package httpx_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	httpx.NewRouter(quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if p := decodeProblem(t, rec); rec.Code != 404 || p.Code != "http.not_found" || p.Status != 404 {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

func TestWrongMethodIsProblem(t *testing.T) {
	r := httpx.NewRouter(quiet())
	r.Get("/x", func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if p := decodeProblem(t, rec); rec.Code != 405 || p.Code != "http.method_not_allowed" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
}

func TestPanicIsProblemAndServerSurvives(t *testing.T) {
	r := httpx.NewRouter(quiet())
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("бум") })
	r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if p := decodeProblem(t, rec); rec.Code != 500 || p.Code != "internal" {
		t.Fatalf("got %d %+v", rec.Code, p)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if rec.Code != 204 {
		t.Fatalf("после паники: %d", rec.Code)
	}
}

func TestProblemCarriesRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	req.Header.Set("X-Request-Id", "req-42")
	rec := httptest.NewRecorder()
	httpx.NewRouter(quiet()).ServeHTTP(rec, req)
	if p := decodeProblem(t, rec); p.RequestID != "req-42" {
		t.Fatalf("request_id = %q", p.RequestID)
	}
}

func TestServeStopsGracefullyOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
