package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wf/backend/internal/platform/health"
)

var next = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, nil))
	return rec
}

func TestHealthzDoesNotTouchDependencies(t *testing.T) {
	called := false
	h := health.Handler(func(context.Context) error { called = true; return errors.New("база лежит") }, next)
	rec := do(h, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) || called {
		t.Fatalf("/healthz: %d %s, ready вызван: %v", rec.Code, rec.Body.String(), called)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestReadyzReflectsDependency(t *testing.T) {
	up := health.Handler(func(context.Context) error { return nil }, next)
	if rec := do(up, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("готов: %d", rec.Code)
	}
	down := health.Handler(func(context.Context) error { return errors.New("dial tcp 10.0.0.5:5432: секрет") }, next)
	rec := do(down, http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatalf("не готов: %d %s — нужен 503 без текста ошибки", rec.Code, rec.Body.String())
	}
}

func TestReadyzHasDeadline(t *testing.T) {
	h := health.Handler(func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("нет дедлайна")
		}
		return nil
	}, next)
	if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("ready без дедлайна: %d", rec.Code)
	}
}

func TestOtherRequestsGoToNext(t *testing.T) {
	h := health.Handler(func(context.Context) error { return nil }, next)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/health"},
		{http.MethodPost, "/healthz"},
		{http.MethodGet, "/healthz/x"},
	} {
		if rec := do(h, c.method, c.path); rec.Code != http.StatusTeapot {
			t.Errorf("%s %s: %d — ждали обработку next", c.method, c.path, rec.Code)
		}
	}
	if rec := do(h, http.MethodHead, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("HEAD /healthz: %d", rec.Code)
	}
}

// Открытый вопрос плана 2/3: /readyz публичный — поток запросов не должен превращаться в поток
// пингов базы. Результат кэшируется на ttl, одновременные пробы схлопываются.
func TestCachedReady(t *testing.T) {
	var calls atomic.Int32
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	ready := health.Cached(func(context.Context) error { calls.Add(1); time.Sleep(20 * time.Millisecond); return nil }, time.Second, clock)
	h := health.Handler(ready, next)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if rec := do(h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
				t.Errorf("status = %d", rec.Code)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("пингов %d на 50 проб", calls.Load())
	}
	now = now.Add(time.Second + time.Millisecond)
	do(h, http.MethodGet, "/readyz")
	if calls.Load() != 2 {
		t.Fatalf("после ttl пингов %d, нужно 2", calls.Load())
	}
}

func TestReadyzExactBodiesAndHead(t *testing.T) {
	down := health.Handler(func(context.Context) error { return errors.New("x") }, next)
	if rec := do(down, http.MethodGet, "/readyz"); rec.Body.String() != `{"status":"unavailable"}` {
		t.Fatalf("тело 503: %q", rec.Body.String())
	}
	up := health.Handler(func(context.Context) error { return nil }, next)
	if rec := do(up, http.MethodHead, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("HEAD /readyz: %d", rec.Code)
	}
}
