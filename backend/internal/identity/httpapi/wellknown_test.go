package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identityhttp "wf/backend/internal/identity/httpapi"
)

func wkGet(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, nil))
	return rec
}

// Отпечатки SHA-256 сертификатов подписи: 32 байта в hex через «:».
var (
	fpA = strings.TrimSuffix(strings.Repeat("AA:", 32), ":")
	fpC = strings.TrimSuffix(strings.Repeat("0c:", 32), ":")
)

func mustWellKnown(t *testing.T, apple []string, android string) http.Handler {
	t.Helper()
	h, err := identityhttp.WellKnown(apple, android)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// §9.4, К4: AASA и assetlinks по конфигу — JSON, application/json, без редиректов; пустой конфиг, чужой путь и метод
// — 404.
func TestWellKnown(t *testing.T) {
	t.Parallel()
	h := mustWellKnown(t, []string{"TEAM1.app.wf", " TEAM1.app.wf.dev "}, "app.wf="+fpA+"; app.wf.dev = "+fpC)
	for path, want := range map[string]string{
		"/.well-known/apple-app-site-association": `{"webcredentials":{"apps":["TEAM1.app.wf","TEAM1.app.wf.dev"]}}`,
		"/.well-known/assetlinks.json": `[{"relation":["delegate_permission/common.get_login_creds"],"target":` +
			`{"namespace":"android_app","package_name":"app.wf","sha256_cert_fingerprints":["` + fpA + `"]}},` +
			`{"relation":["delegate_permission/common.get_login_creds"],"target":` +
			`{"namespace":"android_app","package_name":"app.wf.dev","sha256_cert_fingerprints":["` + fpC + `"]}}]`,
	} {
		rec := wkGet(h, http.MethodGet, path)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != want {
			t.Fatalf("%s: %d %v %s", path, rec.Code, rec.Header(), rec.Body.String())
		}
	}
	empty := mustWellKnown(t, nil, "")
	for _, c := range []struct {
		h            http.Handler
		method, path string
	}{
		{empty, http.MethodGet, "/.well-known/apple-app-site-association"},
		{empty, http.MethodGet, "/.well-known/assetlinks.json"},
		{h, http.MethodGet, "/.well-known/apple-app-site-association/"},
		{h, http.MethodGet, "/.well-known/other.json"},
		{h, http.MethodPost, "/.well-known/assetlinks.json"},
	} {
		if rec := wkGet(c.h, c.method, c.path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
}

// Битый API_WELLKNOWN_ANDROID — ошибка сборки с именем ключа (отказ старта в cmd/api), не паника: нет пакета или
// отпечатка; отпечаток не SHA-256 — не 32 байта, не hex, без «:» или с лишним разделителем.
func TestWellKnownRejectsBadAndroid(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ why, bad string }{
		{"нет отпечатка", "app.wf"},
		{"нет пакета", "=" + fpA},
		{"пустой отпечаток", "app.wf="},
		{"2 байта", "app.wf=AA:BB"},
		{"33 байта", "app.wf=" + fpA + ":AA"},
		{"без разделителей", "app.wf=" + strings.ReplaceAll(fpA, ":", "")},
		{"не hex", "app.wf=" + strings.Replace(fpA, "AA", "AG", 1)},
		{"разделитель не на месте", "app.wf=" + strings.Replace(fpA, "AA:", "A:A", 1)},
		{"вместо разделителя — hex-цифра", "app.wf=" + strings.Replace(fpA, ":", "A", 1)},
		{"висячий разделитель", "app.wf=" + fpA + ":"},
		{"вторая запись битая", "app.wf=" + fpA + ";x=AA"},
	} {
		h, err := identityhttp.WellKnown(nil, c.bad)
		if err == nil || h != nil || !strings.Contains(err.Error(), "API_WELLKNOWN_ANDROID") {
			t.Errorf("%s (%q): %v %v", c.why, c.bad, h, err)
		}
	}
}
