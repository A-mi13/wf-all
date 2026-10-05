package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"wf/backend/internal/platform/httpx"
)

// Несколько строк заголовка из списка — одна через ", " (RFC 9110 §5.3); одна строка, отсутствие
// и чужие заголовки — без изменений. Имя — без учёта регистра.
func TestCombineHeaders(t *testing.T) {
	var got http.Header
	h := httpx.CombineHeaders("accept-language")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	serve := func(set func(http.Header)) http.Header {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		set(r.Header)
		h.ServeHTTP(httptest.NewRecorder(), r)
		return got
	}

	g := serve(func(hd http.Header) {
		hd.Add("Accept-Language", "fr")
		hd.Add("Accept-Language", "en;q=0.5")
		hd.Add("X-Other", "a")
		hd.Add("X-Other", "b")
	})
	if v := g.Values("Accept-Language"); !slices.Equal(v, []string{"fr, en;q=0.5"}) {
		t.Fatalf("Accept-Language = %q", v)
	}
	if v := g.Values("X-Other"); !slices.Equal(v, []string{"a", "b"}) {
		t.Fatalf("чужой заголовок изменён: %q", v)
	}

	g = serve(func(hd http.Header) { hd.Set("Accept-Language", "ru-RU,ru;q=0.9") })
	if v := g.Values("Accept-Language"); !slices.Equal(v, []string{"ru-RU,ru;q=0.9"}) {
		t.Fatalf("одна строка изменена: %q", v)
	}

	g = serve(func(http.Header) {})
	if _, ok := g["Accept-Language"]; ok {
		t.Fatal("заголовка не было, а он появился")
	}
}
