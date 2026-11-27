package public_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/httpapi/public"
)

// fakeDevMedia — dev-пути хранилища: читает тело целиком и отвечает 204 (чтение сверх лимита — 413).
type fakeDevMedia struct{ limit int64 }

func (f fakeDevMedia) UploadLimit() int64 { return f.limit }

func (fakeDevMedia) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Спека media §6.3, R16: dev-пути (Options.Media) монтируются только заданными — /_blob/in со своим лимитом тела
// (больше общего 1 МиБ), /_media/*; без них — 404. Тестовые ручки (Options.E2E) — так же под /_e2e/.
func TestDevMediaMounted(t *testing.T) {
	t.Parallel()
	o := testOptions(t)
	serve := func(o public.Options, method, path string, size int) int {
		t.Helper()
		h, err := public.NewHandler(slog.New(slog.DiscardHandler), o)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(make([]byte, size))))
		return rec.Code
	}
	const mib = 1 << 20
	for _, c := range []struct {
		method, path string
		size         int
	}{{http.MethodPost, "/_blob/in", 0}, {http.MethodGet, "/_media/pub/x/thumb.webp", 0}, {http.MethodPost, "/_e2e/media/uploads", 0}} {
		if got := serve(o, c.method, c.path, c.size); got != http.StatusNotFound {
			t.Errorf("без Media/E2E %s %s: %d, ждали 404", c.method, c.path, got)
		}
	}
	o.Media = fakeDevMedia{limit: 16 * mib}
	o.E2E = fakeDevMedia{}
	for _, c := range []struct {
		method, path string
		size, want   int
	}{
		{http.MethodPost, "/_blob/in", 6 * mib, http.StatusNoContent},
		{http.MethodPost, "/_blob/in", 16*mib + 1, http.StatusRequestEntityTooLarge},
		{http.MethodGet, "/_media/pub/x/thumb.webp", 0, http.StatusNoContent},
		{http.MethodGet, "/_media/priv/x/full.webp", 0, http.StatusNoContent},
		{http.MethodPost, "/_e2e/media/uploads", mib + 1, http.StatusRequestEntityTooLarge}, // общий лимит
		{http.MethodPost, "/_e2e/media/uploads", 10, http.StatusNoContent},
		{http.MethodPost, "/_blob/inx", 10, http.StatusNotFound},
	} {
		if got := serve(o, c.method, c.path, c.size); got != c.want {
			t.Errorf("%s %s %d байт: %d, ждали %d", c.method, c.path, c.size, got, c.want)
		}
	}
}
