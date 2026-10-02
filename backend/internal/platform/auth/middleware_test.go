package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/token"
)

type fakeVerifier map[string]token.Claims

func (f fakeVerifier) Verify(raw string) (token.Claims, error) {
	if c, ok := f[raw]; ok {
		return c, nil
	}
	return token.Claims{}, token.ErrInvalid
}

func TestMiddleware(t *testing.T) {
	uid, sid, otherUser := uuid.New(), uuid.New(), uuid.New()
	v := fakeVerifier{
		"good":     {UserID: uid, SessionID: sid},
		"revoked":  {UserID: uid, SessionID: uuid.New()},
		"mismatch": {UserID: otherUser, SessionID: sid},
		"broken":   {UserID: uid, SessionID: uuid.Nil},
	}
	loader := auth.SessionLoaderFunc(func(_ context.Context, s uuid.UUID) (*auth.Principal, error) {
		switch s {
		case sid:
			return &auth.Principal{UserID: uid, SessionID: sid, Status: "active"}, nil
		case uuid.Nil:
			return nil, errors.New("база недоступна")
		}
		return nil, auth.ErrSessionInvalid
	})
	cases := []struct {
		name    string
		header  string
		status  int
		code    string
		withWho bool
	}{
		{"без Authorization — дальше без Principal", "", 204, "", false},
		{"действующий токен", "Bearer good", 204, "", true},
		{"схема в другом регистре", "bearer good", 204, "", true},
		{"не Bearer", "Basic Zm9vOmJhcg==", 401, httpx.CodeUnauthenticated, false},
		{"Bearer без токена", "Bearer ", 401, httpx.CodeUnauthenticated, false},
		{"подделка", "Bearer forged", 401, httpx.CodeUnauthenticated, false},
		{"сессия отозвана", "Bearer revoked", 401, httpx.CodeUnauthenticated, false},
		{"sub не совпал с владельцем сессии", "Bearer mismatch", 401, httpx.CodeUnauthenticated, false},
		{"сбой загрузки — 500, не 401", "Bearer broken", 500, httpx.CodeInternal, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			var got *auth.Principal
			h := auth.Middleware(v, loader, slog.New(slog.NewTextHandler(&logs, nil)))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got, _ = auth.From(r.Context())
					w.WriteHeader(http.StatusNoContent)
				}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if c.code != "" {
				var p httpx.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != c.code {
					t.Fatalf("тело: %s", rec.Body.String())
				}
			}
			if c.status == 401 && rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatal("нет WWW-Authenticate: Bearer")
			}
			if c.withWho != (got != nil) || (got != nil && got.UserID != uid) {
				t.Fatalf("Principal: %+v", got)
			}
		})
	}
}

const securitySpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
security:
  - bearer: []
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
paths:
  /anonymous:
    get: {security: [], responses: {"200": {description: ok}}}
  /required:
    get: {responses: {"200": {description: ok}}}
  /optional:
    get: {security: [{}, {bearer: []}], responses: {"200": {description: ok}}}
`

// Маршрут известен: у анонимной операции (в security нет bearer) Authorization не читается —
// клиент с истёкшим токеном, вешающий заголовок на все запросы, не получит 401 на refresh,
// входе и кодах. Операция с bearer (обязательным или опциональным) — битый токен даёт 401.
func TestMiddlewareIgnoresAuthorizationOnAnonymousOperation(t *testing.T) {
	uid, sid := uuid.New(), uuid.New()
	v := fakeVerifier{"good": {UserID: uid, SessionID: sid}}
	loader := auth.SessionLoaderFunc(func(_ context.Context, s uuid.UUID) (*auth.Principal, error) {
		if s == sid {
			return &auth.Principal{UserID: uid, SessionID: sid, Status: "active"}, nil
		}
		return nil, auth.ErrSessionInvalid
	})
	spec, err := openapi3.NewLoader().LoadFromData([]byte(securitySpec))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(spec)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path, header string
		status             int
		withWho            bool
	}{
		{"анонимная + битый токен — анонимно", "/anonymous", "Bearer forged", 204, false},
		{"анонимная + не Bearer — анонимно", "/anonymous", "Basic Zm9vOmJhcg==", 204, false},
		{"анонимная + действующий токен — Principal не кладётся", "/anonymous", "Bearer good", 204, false},
		{"обязательный bearer + битый токен — 401", "/required", "Bearer forged", 401, false},
		{"опциональный bearer + битый токен — 401", "/optional", "Bearer forged", 401, false},
		{"опциональный bearer + действующий токен", "/optional", "Bearer good", 204, true},
		{"маршрута нет + битый токен — 401, как раньше", "/nope", "Bearer forged", 401, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got *auth.Principal
			h := routes(auth.Middleware(v, loader, slog.New(slog.DiscardHandler))(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got, _ = auth.From(r.Context())
					w.WriteHeader(http.StatusNoContent)
				})))
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			req.Header.Set("Authorization", c.header)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if c.withWho != (got != nil) {
				t.Fatalf("Principal: %+v", got)
			}
		})
	}
}

// Паника загрузчика за кэшем (чужая горутина singleflight — recoverer сервера её не видит):
// 500 на этот запрос, процесс жив.
func TestMiddlewareLoaderPanic(t *testing.T) {
	uid, sid := uuid.New(), uuid.New()
	v := fakeVerifier{"good": {UserID: uid, SessionID: sid}}
	next := auth.SessionLoaderFunc(func(context.Context, uuid.UUID) (*auth.Principal, error) {
		panic("загрузчик сломан")
	})
	l := auth.NewCachedLoader(next, 5*time.Second, clocktest.New(time.Now()), 100)
	called := false
	h := auth.Middleware(v, l, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var p httpx.Problem
	if rec.Code != http.StatusInternalServerError || called ||
		json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.Code != httpx.CodeInternal {
		t.Fatalf("status = %d, хендлер вызван: %v, тело: %s", rec.Code, called, rec.Body.String())
	}
}

// Загрузчик нарушил договор — ни Principal, ни ошибки: 500, а не паника и не вход.
func TestMiddlewareNilPrincipal(t *testing.T) {
	uid, sid := uuid.New(), uuid.New()
	v := fakeVerifier{"good": {UserID: uid, SessionID: sid}}
	loader := auth.SessionLoaderFunc(func(context.Context, uuid.UUID) (*auth.Principal, error) {
		return nil, nil
	})
	called := false
	h := auth.Middleware(v, loader, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || called {
		t.Fatalf("status = %d, хендлер вызван: %v", rec.Code, called)
	}
}
