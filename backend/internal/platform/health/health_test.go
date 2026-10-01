package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
