package public_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity"
	identityhttp "wf/backend/internal/identity/httpapi"
)

// pendingSessions — операции области в pendingOps (раскладка — план I-2, Task 8, R5): реализовала задача операцию — убрать
// её отсюда (TestNoStubOperations: «убрать из pendingOps»). Владелец — Task 12 плана I-2: все четыре реализованы.
var pendingSessions = []string{}

// «Где вы вошли» по контракту: список с current и private, no-store; DELETE своей — 204 (с Idempotency-Key), повтор
// и чужая — 404 identity.session_not_found; revoke-others — {revoked: n}; без Idempotency-Key мутации — 400.
func TestSessionsHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	e.seedLogin(t, "http_sessions", "Офсайд на 12-й")
	e.seedLogin(t, "http_sessions_other", "Офсайд на 13-й")
	first := e.login(t, "http_sessions", "Офсайд на 12-й")
	second := e.login(t, "http_sessions", "Офсайд на 12-й")
	third := e.login(t, "http_sessions", "Офсайд на 12-й")
	other := e.login(t, "http_sessions_other", "Офсайд на 13-й")
	access := first.Tokens.AccessToken

	rec := e.do(t, http.MethodGet, "/v1/me/sessions", "", access, false)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("список: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var list oapi.SessionList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 3 {
		t.Fatalf("сессий %d", len(list.Items))
	}
	for _, s := range list.Items {
		if s.Current != (s.Id == first.Tokens.SessionId) || s.Platform == nil || *s.Platform != "ios" || s.Browser != nil {
			t.Fatalf("%+v", s)
		}
	}
	for _, sid := range []uuid.UUID{other.Tokens.SessionId, uuid.New()} {
		rec := e.do(t, http.MethodDelete, "/v1/me/sessions/"+sid.String(), "", access, true)
		if rec.Code != http.StatusNotFound || problemCodeOf(t, rec) != identity.CodeSessionNotFound {
			t.Fatalf("чужая/нет: %d %s", rec.Code, rec.Body.String())
		}
	}
	path := "/v1/me/sessions/" + second.Tokens.SessionId.String()
	if rec := e.do(t, http.MethodDelete, path, "", access, true); rec.Code != http.StatusNoContent {
		t.Fatalf("своя: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodDelete, path, "", access, true); rec.Code != http.StatusNotFound {
		t.Fatalf("повтор: %d %s", rec.Code, rec.Body.String())
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/me/sessions/revoke-others", nil)
	r.Header.Set("Authorization", "Bearer "+access)
	if rec := httptest.NewRecorder(); func() bool { e.h.ServeHTTP(rec, r); return rec.Code != http.StatusBadRequest }() {
		t.Fatalf("revoke-others без Idempotency-Key: %d", rec.Code)
	}
	rec = e.do(t, http.MethodPost, "/v1/me/sessions/revoke-others", "", access, true)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" || rec.Body.String() != "{\"revoked\":1}\n" {
		t.Fatalf("revoke-others: %d %s", rec.Code, rec.Body.String())
	}
	_ = third
}

// getAuthMethods анонимно: 200, public, max-age=60, password всегда true; провайдеров у api нет — остальное false.
func TestAuthMethodsHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityEnv(t)
	rec := e.do(t, http.MethodGet, "/v1/auth/methods", "", "", false)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var v oapi.AuthMethods
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v != (oapi.AuthMethods{Password: true}) {
		t.Fatalf("%+v", v)
	}
}

// R14: /.well-known/* монтируется до маршрутизатора контракта — вне контракта, без валидатора и лимитов (лимитер
// «всё запрещено» его не трогает); без Options.WellKnown — 404 Problem.
func TestWellKnownMounted(t *testing.T) {
	t.Parallel()
	o := testOptions(t)
	o.Limiter = denyAll{}
	wk, err := identityhttp.WellKnown([]string{"TEAM1.app.wf"}, "app.wf="+strings.TrimSuffix(strings.Repeat("AA:", 32), ":"))
	if err != nil {
		t.Fatal(err)
	}
	o.WellKnown = wk
	h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/.well-known/apple-app-site-association", "/.well-known/assetlinks.json"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s: %d %v %s", path, rec.Code, rec.Header(), rec.Body.String())
		}
	}
	o.WellKnown = nil
	h, err = public.NewHandler(slog.New(slog.DiscardHandler), o)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/.well-known/assetlinks.json", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("без WellKnown: %d", rec.Code)
	}
}
