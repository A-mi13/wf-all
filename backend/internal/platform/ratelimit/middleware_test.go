package ratelimit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
)

const spec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /login:
    post: {operationId: login, x-rate-limit: auth, responses: {"204": {description: ok}}}
  /feed:
    get: {operationId: feed, responses: {"204": {description: ok}}}
`

func pipeline(t *testing.T, l ratelimit.Limiter, rules ratelimit.Rules, logs *bytes.Buffer) http.Handler {
	t.Helper()
	s, err := openapi3.NewLoader().LoadFromData([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpx.Routes(s)
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return routes(ratelimit.Middleware(l, rules, slog.New(slog.NewTextHandler(logs, nil)))(ok))
}

func do(h http.Handler, method, path, ip string, p *auth.Principal) *httptest.ResponseRecorder {
	return doDevice(h, method, path, ip, "", p)
}

func doDevice(h http.Handler, method, path, ip, device string, p *auth.Principal) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	ctx := peer.With(r.Context(), peer.Info{IP: netip.MustParseAddr(ip), Device: device})
	if p != nil {
		ctx = auth.With(ctx, p)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

func TestMiddleware(t *testing.T) {
	l, c, _ := limiter(t)
	rules := ratelimit.Rules{
		"default": {IP: ratelimit.Policy{Limit: 100, Period: time.Minute}, User: ratelimit.Policy{Limit: 2, Period: time.Minute}},
		"auth":    {IP: ratelimit.Policy{Limit: 2, Period: time.Minute}},
	}
	var logs bytes.Buffer
	h := pipeline(t, l, rules, &logs)

	// класс из x-rate-limit: два входа с IP, третий — 429
	for range 2 {
		if rec := do(h, http.MethodPost, "/login", "203.0.113.1", nil); rec.Code != 204 {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	rec := do(h, http.MethodPost, "/login", "203.0.113.1", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("третий вход: %d", rec.Code)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != httpx.CodeRateLimited {
		t.Fatalf("тело: %s", rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "30" {
		t.Fatalf("Retry-After = %q, нужно 30", ra)
	}
	// Review Focus 1: соседний IP не задет — счёт по реальному IP из peer
	if rec := do(h, http.MethodPost, "/login", "203.0.113.2", nil); rec.Code != 204 {
		t.Fatalf("другой IP: %d", rec.Code)
	}
	// классы не делят счёт: лента с того же IP идёт по default
	if rec := do(h, http.MethodGet, "/feed", "203.0.113.1", nil); rec.Code != 204 {
		t.Fatalf("лента: %d", rec.Code)
	}

	// по пользователю — с разных IP
	who := &auth.Principal{UserID: uuid.New()}
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		want := 204
		if i == 2 {
			want = 429
		}
		if rec := do(h, http.MethodGet, "/feed", ip, who); rec.Code != want {
			t.Fatalf("пользователь, запрос %d: %d", i+1, rec.Code)
		}
	}
	c.Advance(time.Minute)
	if rec := do(h, http.MethodGet, "/feed", "198.51.100.4", who); rec.Code != 204 {
		t.Fatalf("через минуту: %d", rec.Code)
	}
}

// Устройство: метка BFF у анонима, устройство сессии у пользователя (оно важнее метки BFF).
func TestMiddlewareDevice(t *testing.T) {
	l, _, _ := limiter(t)
	rules := ratelimit.Rules{
		"default": {Device: ratelimit.Policy{Limit: 1, Period: time.Minute}},
	}
	var logs bytes.Buffer
	h := pipeline(t, l, rules, &logs)

	// аноним с меткой BFF: второй запрос с того же устройства — 429 даже с другого IP
	if rec := doDevice(h, http.MethodGet, "/feed", "203.0.113.1", "dev-a", nil); rec.Code != 204 {
		t.Fatalf("первый: %d", rec.Code)
	}
	if rec := doDevice(h, http.MethodGet, "/feed", "203.0.113.2", "dev-a", nil); rec.Code != 429 {
		t.Fatalf("то же устройство: %d", rec.Code)
	}
	if rec := doDevice(h, http.MethodGet, "/feed", "203.0.113.2", "dev-b", nil); rec.Code != 204 {
		t.Fatalf("другое устройство: %d", rec.Code)
	}

	// пользователь: считается устройство сессии, метка BFF не важна
	who := &auth.Principal{UserID: uuid.New(), DeviceID: uuid.New()}
	if rec := doDevice(h, http.MethodGet, "/feed", "198.51.100.1", "dev-c", who); rec.Code != 204 {
		t.Fatalf("сессия, первый: %d", rec.Code)
	}
	if rec := doDevice(h, http.MethodGet, "/feed", "198.51.100.1", "dev-d", who); rec.Code != 429 {
		t.Fatalf("сессия, то же устройство с другой меткой BFF: %d", rec.Code)
	}
}

// Маршрут не из контракта лимитом не считается — его ответит роутер.
func TestMiddlewareSkipsUnknownRoute(t *testing.T) {
	var logs bytes.Buffer
	h := pipeline(t, failing{}, ratelimit.DefaultRules(), &logs)
	if rec := do(h, http.MethodGet, "/docs", "203.0.113.1", nil); rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
	if logs.Len() != 0 {
		t.Fatalf("лимитер вызван для маршрута не из контракта: %s", logs.String())
	}
}

type failing struct{}

func (failing) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{}, errors.New("база недоступна")
}
func (failing) Add(context.Context, string, ratelimit.Policy) error { return errors.New("x") }
func (failing) Over(context.Context, string, ratelimit.Policy) (bool, error) {
	return false, errors.New("x")
}

// Хранилище лимитов недоступно — запрос проходит, ошибка — в лог (fail-open).
func TestMiddlewareFailOpen(t *testing.T) {
	var logs bytes.Buffer
	h := pipeline(t, failing{}, ratelimit.DefaultRules(), &logs)
	if rec := do(h, http.MethodGet, "/feed", "203.0.113.1", nil); rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "база недоступна") {
		t.Fatalf("ошибки нет в логе: %s", logs.String())
	}
}
