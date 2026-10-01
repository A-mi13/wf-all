package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"wf/backend/internal/platform/httpx"
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != httpx.ContentTypeProblem {
		t.Fatalf("Content-Type = %q", ct)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("тело не JSON: %v: %s", err, rec.Body.String())
	}
	return p
}

func TestUnknownRouteIsProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.NewRouter(quiet()).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil))
	if p := decodeProblem(t, rec); rec.Code != http.StatusNotFound || p.Code != "http.not_found" || p.Status != http.StatusNotFound {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

func TestWrongMethodIsProblem(t *testing.T) {
	r := httpx.NewRouter(quiet())
	r.Get("/x", func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil))
	if p := decodeProblem(t, rec); rec.Code != http.StatusMethodNotAllowed || p.Code != "http.method_not_allowed" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

func TestPanicIsProblemAndServerSurvives(t *testing.T) {
	r := httpx.NewRouter(quiet())
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("бум") })
	r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	if p := decodeProblem(t, rec); rec.Code != http.StatusInternalServerError || p.Code != "internal" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("после паники: %d", rec.Code)
	}
}

func TestProblemCarriesRequestID(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil)
	req.Header.Set("X-Request-Id", "req-42")
	rec := httptest.NewRecorder()
	httpx.NewRouter(quiet()).ServeHTTP(rec, req)
	if p := decodeProblem(t, rec); p.RequestID != "req-42" {
		t.Fatalf("request_id = %q", p.RequestID)
	}
}

func TestRecovererWorksOnEmptyRouter(t *testing.T) {
	r := httpx.NewRouter(quiet())
	r.NotFound(func(http.ResponseWriter, *http.Request) { panic("бум") })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil))
	if p := decodeProblem(t, rec); rec.Code != http.StatusInternalServerError || p.Code != "internal" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

// Middleware бинарника стоит после общих (видит request id) и до маршрутов — включая 404.
func TestRouterRunsBinaryMiddlewareAfterCommon(t *testing.T) {
	var seenID string
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenID = middleware.GetReqID(r.Context())
			next.ServeHTTP(w, r)
		})
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil)
	req.Header.Set("X-Request-Id", "req-7")
	rec := httptest.NewRecorder()
	httpx.NewRouter(quiet(), mw).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || seenID != "req-7" {
		t.Fatalf("status = %d, request id в middleware бинарника = %q", rec.Code, seenID)
	}
}

// Паника в middleware бинарника ловится общим recoverer — он стоит раньше.
func TestRouterRecoversBinaryMiddlewarePanic(t *testing.T) {
	boom := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("бум") })
	}
	rec := httptest.NewRecorder()
	httpx.NewRouter(quiet(), boom).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil))
	if p := decodeProblem(t, rec); rec.Code != http.StatusInternalServerError || p.Code != "internal" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

// Ошибка разбора запроса в сгенерированном коде: клиенту — problem+json без текста ошибки,
// текст — в лог.
func TestRequestErrorHandlerHidesErrorText(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	httpx.RequestErrorHandler(log)(rec, req, errors.New("внутренности биндинга"))
	p := decodeProblem(t, rec)
	if rec.Code != http.StatusBadRequest || p.Code != "request.invalid" || p.Detail != "" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
	if strings.Contains(rec.Body.String(), "внутренности") || !strings.Contains(logs.String(), "внутренности биндинга") {
		t.Fatalf("текст ошибки: ответ = %s, лог = %s", rec.Body.String(), logs.String())
	}
}

func TestServeStopsGracefullyOnCancel(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- httpx.Serve(ctx, ln, http.NotFoundHandler(), time.Second) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve не остановился")
	}
}
