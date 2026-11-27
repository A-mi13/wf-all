package admin_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/platform/httpx"
)

// bodyReader — тестовые ручки: читают тело целиком (сверх лимита — 413).
type bodyReader struct{}

func (bodyReader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Тестовые ручки wf_e2e (Options.E2E) — под /_e2e/ за общим конвейером: CSRF у мутаций, свои исключения лимита тела
// (R16); без них маршрута нет.
func TestE2ERoutesMounted(t *testing.T) {
	t.Parallel()
	const mib = 1 << 20
	o := testOptions(t)
	req := func(size int, csrf bool) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/_e2e/media/direct",
			bytes.NewReader(make([]byte, size)))
		if csrf {
			r.Header.Set("Origin", testOrigin)
			r.Header.Set("X-WF-Admin", "1")
		}
		return r
	}
	wantProblem(t, serve(newHandler(t, o), req(10, true)), http.StatusNotFound, httpx.CodeNotFound)

	o.E2E = bodyReader{}
	o.E2EBodyLimits = []httpx.BodyLimit{{Method: http.MethodPost, Prefix: "/_e2e/media/direct", Max: 5 * mib}}
	h := newHandler(t, o)
	wantProblem(t, serve(h, req(10, false)), http.StatusForbidden, httpx.CodeCSRFFailed)
	if rec := serve(h, req(5*mib, true)); rec.Code != http.StatusNoContent {
		t.Fatalf("5 МиБ: %d", rec.Code)
	}
	if rec := serve(h, req(5*mib+1, true)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("5 МиБ + 1: %d", rec.Code)
	}
}
