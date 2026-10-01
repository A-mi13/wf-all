package apidocs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"wf/backend/internal/platform/apidocs"
)

const spec = `{"openapi":"3.0.3","info":{"title":"T","version":"1"}}`

func serve(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	apidocs.Mount(r, "T API", []byte(spec))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func TestPageLoadsPinnedSwaggerUIWithIntegrity(t *testing.T) {
	rec := serve(t, "/docs")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d, content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<title>T API</title>",
		`src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@` + apidocs.SwaggerUIVersion + `/swagger-ui-bundle.js"`,
		`integrity="sha384-`,
		`src="/docs/init.js"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("в странице нет %q:\n%s", want, body)
		}
	}
	// без integrity у каждого внешнего ресурса подмена файла на CDN исполнилась бы у нас
	if n := strings.Count(body, "https://cdn.jsdelivr.net/"); n != strings.Count(body, `integrity="sha384-`) {
		t.Errorf("внешних ресурсов %d, а integrity — %d", n, strings.Count(body, `integrity="sha384-`))
	}
}

func TestPageForbidsInlineScriptsAndEval(t *testing.T) {
	csp := serve(t, "/docs").Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self' https://cdn.jsdelivr.net;", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q без %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP разрешает eval: %q", csp)
	}
}

func TestSpecServedAsIs(t *testing.T) {
	rec := serve(t, "/docs/openapi.json")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status = %d, content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != spec {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestInitScriptPointsToSpec(t *testing.T) {
	rec := serve(t, "/docs/init.js")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("status = %d, content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "'"+apidocs.Path+"/openapi.json'") {
		t.Fatalf("init.js не ссылается на контракт: %s", rec.Body.String())
	}
}

func TestEveryResponseIsNosniff(t *testing.T) {
	for _, p := range []string{"/docs", "/docs/openapi.json", "/docs/init.js"} {
		if got := serve(t, p).Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", p, got)
		}
	}
}

func TestTrailingSlashRedirects(t *testing.T) {
	rec := serve(t, "/docs/")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/docs" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
}
