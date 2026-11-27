package httpx_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"wf/backend/internal/platform/httpx"
)

// R16 плана media: исключение из общего лимита тела — ровно метод и префикс пути (до «/» или конца строки); прочее —
// общий лимит.
func TestLimitBodyExcept(t *testing.T) {
	t.Parallel()
	const mib = 1 << 20
	h := httpx.LimitBody(mib, httpx.BodyLimit{Method: http.MethodPost, Prefix: "/_blob/in", Max: 16 * mib})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	cases := []struct {
		method, path string
		size         int
		want         int
	}{
		{http.MethodPost, "/_blob/in", 6 * mib, http.StatusNoContent},
		{http.MethodPost, "/_blob/in/x", 6 * mib, http.StatusNoContent}, // префикс до «/»
		{http.MethodPost, "/_blob/in", 16*mib + 1, http.StatusRequestEntityTooLarge},
		{http.MethodPut, "/_blob/in", 6 * mib, http.StatusRequestEntityTooLarge},
		{http.MethodPost, "/_blob/inx", 6 * mib, http.StatusRequestEntityTooLarge},
		{http.MethodPost, "/_blob", 6 * mib, http.StatusRequestEntityTooLarge},
		{http.MethodPost, "/v1/x", mib, http.StatusNoContent},
		{http.MethodPost, "/v1/x", mib + 1, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), c.method, c.path, bytes.NewReader(make([]byte, c.size))))
		if rec.Code != c.want {
			t.Errorf("%s %s %d байт: %d, ждали %d", c.method, c.path, c.size, rec.Code, c.want)
		}
	}
}
