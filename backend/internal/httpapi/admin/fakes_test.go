package admin_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	staffadmin "wf/backend/internal/staff/admin"
)

const (
	testOrigin = "https://admin.wf.example"
	clientIP   = "192.0.2.1" // адрес httptest-запроса (RemoteAddr 192.0.2.1:1234)
	hostCookie = "__Host-wf_admin_session"
)

var (
	cityA = uuid.MustParse("0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d")
	cityB = uuid.MustParse("0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6e")
	t0    = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
)

// testOptions — рабочий набор зависимостей: лимиты пропускают всё, база — чистая
// (идемпотентность), сессий нет (fakeSessions без токенов), статики нет.
func testOptions(t *testing.T) admin.Options {
	t.Helper()
	return admin.Options{
		DB:           dbtest.NewPool(t),
		Limiter:      ratelimit.Unlimited,
		RateRules:    ratelimit.DefaultRules(),
		Origin:       testOrigin,
		CookieSecure: true,
		Sessions:     &fakeSessions{},
		Staff:        &staffadmin.Handler{},
		Geo:          &geoadmin.Handler{},
		Clock:        clocktest.New(t0),
	}
}

func newHandler(t *testing.T, o admin.Options) http.Handler {
	t.Helper()
	h, err := admin.NewHandler(quiet(), o)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// staffOf — сотрудник роли role со своими правами из матрицы (perm.Grants).
func staffOf(role perm.Role, stage string, reauth bool, cities ...uuid.UUID) *auth.Staff {
	return &auth.Staff{
		StaffID: uuid.New(), SessionID: uuid.New(), Role: string(role), CityIDs: cities,
		Stage: stage, Reauth: reauth, Grants: perm.Grants(role),
	}
}

type loadCall struct {
	token      string
	ip         netip.Addr
	background bool
}

// fakeSessions — auth.StaffSessions по таблице токенов: нет токена — ErrSessionInvalid, err
// задан — он на любой токен. Запоминает вызовы.
type fakeSessions struct {
	staff map[string]*auth.Staff
	err   error

	mu    sync.Mutex
	calls []loadCall
}

func (f *fakeSessions) LoadStaff(_ context.Context, token string, ip netip.Addr, background bool) (*auth.Staff, error) {
	f.mu.Lock()
	f.calls = append(f.calls, loadCall{token, ip, background})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if s, ok := f.staff[token]; ok {
		return s, nil
	}
	return nil, auth.ErrSessionInvalid
}

func (f *fakeSessions) loads() []loadCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]loadCall(nil), f.calls...)
}

// recLimiter — ratelimit.Limiter, который пропускает всё и запоминает ключи Allow и Add; over —
// ответ Over.
type recLimiter struct {
	over bool

	mu           sync.Mutex
	allows, adds []string
}

func (l *recLimiter) Allow(_ context.Context, key string, _ ratelimit.Policy) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.allows = append(l.allows, key)
	return ratelimit.Result{Allowed: true}, nil
}

func (l *recLimiter) Add(_ context.Context, key string, _ ratelimit.Policy) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.adds = append(l.adds, key)
	return nil
}

func (l *recLimiter) Over(context.Context, string, ratelimit.Policy) (bool, error) {
	return l.over, nil
}

// call — запрос админки: cookie сессии (token непустой) и, у изменяющих методов, Origin
// админки, X-WF-Admin: 1 и Idempotency-Key.
func call(method, path, token, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequestWithContext(context.Background(), method, path, rd)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.AddCookie(sessionCookie(hostCookie, token))
	}
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("X-WF-Admin", "1")
		r.Header.Set("Idempotency-Key", uuid.NewString())
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// wantProblem — ответ — Problem со статусом и кодом.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("ждали %d %s, получили %d %s", status, code, rec.Code, rec.Body.String())
	}
}

// wantPassed — запрос прошёл весь конвейер и дошёл до заглушки хендлера (501 http.not_implemented).
func wantPassed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantProblem(t, rec, http.StatusNotImplemented, httpx.CodeNotImplemented)
}

// sessionCookie — cookie сессии в запросе. В заголовок Cookie уходят только имя и значение;
// атрибуты — как у установки (gosec G124 смотрит на литерал).
func sessionCookie(name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}
