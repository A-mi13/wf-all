package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/httpx"
)

const (
	indexHTML = `<!doctype html><title>admin</title>`
	appJS     = `console.log("admin")`
	outside   = `файл вне каталога: не-для-браузера`
)

// staticDir — собранная apps/admin во временном каталоге; рядом с каталогом (уровнем выше) —
// файл-секрет: выход за каталог его отдал бы.
func staticDir(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "dist")
	for name, body := range map[string]string{
		filepath.Join(parent, "secret.env"):           outside,
		filepath.Join(dir, "index.html"):              indexHTML,
		filepath.Join(dir, "favicon.svg"):             `<svg xmlns="http://www.w3.org/2000/svg"/>`,
		filepath.Join(dir, "assets", "app-3f2c8e.js"): appJS,
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.URL.Path = path // как есть: httptest.NewRequest нормализует и отвергает часть путей
	r.RequestURI = path
	return serve(h, r)
}

// wantSecurityHeaders — заголовки безопасности статики (спека §5.2).
func wantSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") ||
		!strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("заголовки %v", h)
	}
}

// Статика из ADMIN_STATIC_DIR (спека §5.2): файл — как есть, остальное вне /v1 — index.html (SPA);
// index.html — no-store, assets/ — immutable; у всех — CSP, nosniff, Referrer-Policy.
func TestStaticServesAdminApp(t *testing.T) {
	o := testOptions(t)
	o.StaticDir = staticDir(t)
	h := newHandler(t, o)

	for _, path := range []string{"/", "/index.html", "/staff/accounts", "/approvals/0192f1e2"} {
		rec := get(h, path)
		if rec.Code != http.StatusOK || rec.Body.String() != indexHTML {
			t.Fatalf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: %v", path, rec.Header())
		}
		wantSecurityHeaders(t, rec)
	}

	rec := get(h, "/assets/app-3f2c8e.js")
	if rec.Code != http.StatusOK || rec.Body.String() != appJS {
		t.Fatalf("asset: %d %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control %q", cc)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Errorf("asset Content-Type %q", rec.Header().Get("Content-Type"))
	}
	wantSecurityHeaders(t, rec)

	if rec := get(h, "/favicon.svg"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("favicon: %d %v", rec.Code, rec.Header())
	}
	// нет файла в assets/ — 404, а не index.html под видом скрипта
	wantProblem(t, get(h, "/assets/missing.js"), http.StatusNotFound, httpx.CodeNotFound)
	// /v1/* — API: маршрута нет — Problem, не статика
	wantProblem(t, get(h, "/v1/nope"), http.StatusNotFound, httpx.CodeNotFound)
	wantProblem(t, get(h, "/v1"), http.StatusNotFound, httpx.CodeNotFound)
	// статика только читается
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/index.html", nil)
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("X-WF-Admin", "1")
	wantProblem(t, serve(h, r), http.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed)
}

// Выход за каталог статики невозможен: .., закодированные слэши, обратные слэши, абсолютные
// пути и диски Windows — ни один запрос не отдаёт файл вне ADMIN_STATIC_DIR.
func TestStaticNoEscape(t *testing.T) {
	o := testOptions(t)
	dir := staticDir(t)
	o.StaticDir = dir
	h := newHandler(t, o)
	abs := filepath.ToSlash(filepath.Join(filepath.Dir(dir), "secret.env"))
	for _, path := range []string{
		"/../secret.env", "/assets/../../secret.env", "/..%2fsecret.env", "/..\\secret.env",
		"/assets/..\\..\\secret.env", "/" + abs, "//" + abs, "/" + strings.ReplaceAll(abs, "/", "\\"),
		"/C:/Windows/win.ini", "/\\\\?\\" + strings.ReplaceAll(abs, "/", "\\"),
	} {
		rec := get(h, path)
		if strings.Contains(rec.Body.String(), "не-для-браузера") {
			t.Errorf("%s: отдан файл вне каталога", path)
		}
		if rec.Code == http.StatusOK && rec.Body.String() != indexHTML {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
}

// Без ADMIN_STATIC_DIR статики нет: вне /v1 — обычный 404 Problem. Каталог без index.html или
// несуществующий — отказ старта.
func TestStaticOffAndBadDir(t *testing.T) {
	base := testOptions(t)
	wantProblem(t, get(newHandler(t, base), "/"), http.StatusNotFound, httpx.CodeNotFound)
	for _, dir := range []string{filepath.Join(t.TempDir(), "nope"), t.TempDir()} {
		o := base
		o.StaticDir = dir
		if _, err := admin.NewHandler(quiet(), o); err == nil {
			t.Errorf("%s: собрано", dir)
		}
	}
}
