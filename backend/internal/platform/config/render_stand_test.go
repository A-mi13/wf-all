package config_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"

	"go.yaml.in/yaml/v3"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/peer"
)

// renderEnv — переменные wf-api из render.yaml (значения, заданные в файле, а не в панели Render).
func renderEnv(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("../../../../render.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var bp struct {
		Services []struct {
			Name    string `yaml:"name"`
			EnvVars []struct {
				Key   string `yaml:"key"`
				Value string `yaml:"value"`
			} `yaml:"envVars"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &bp); err != nil {
		t.Fatal(err)
	}
	for _, s := range bp.Services {
		if s.Name != "wf-api" {
			continue
		}
		env := map[string]string{}
		for _, e := range s.EnvVars {
			env[e.Key] = e.Value
		}
		return env
	}
	t.Fatal("в render.yaml нет сервиса wf-api")
	return nil
}

// Страж исправления стенда: на бесплатном тарифе Render к приложению подключается локальный прокси
// по петле ([::1], лог peer 02.10.2026), и пока петли не было в API_TRUSTED_PROXIES, заголовок CDN
// не читался — все клиенты получали IP ::1 (общий лимит). Берём настройки так, как они лежат в
// render.yaml, и прогоняем ту же картину запроса.
func TestRenderYAMLClientIP(t *testing.T) {
	stand := renderEnv(t)
	c, err := config.Load[config.API]("API_", []string{
		"API_HTTP_ADDR=:8080", "API_DATABASE_URL=postgres://x", "API_JWT_SEEDS=a", "API_HUMANCHECK_KEYS=k",
		"API_TRUSTED_PROXIES=" + stand["API_TRUSTED_PROXIES"],
		"API_CLIENT_IP_HEADER=" + stand["API_CLIENT_IP_HEADER"],
	})
	if err != nil {
		t.Fatalf("render.yaml: настройки не разбираются: %v", err)
	}
	pc := peer.Config{TrustedProxies: c.Peer.TrustedProxies, ClientIPHeader: c.Peer.ClientIPHeader}
	if err := pc.Validate(); err != nil {
		t.Fatalf("render.yaml: peer.Config невалиден: %v", err)
	}

	var got peer.Info
	h := peer.Middleware(pc)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = peer.From(r.Context()) }))
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:41198"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 172.70.242.31, 10.29.109.133")
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if want := netip.MustParseAddr("203.0.113.7"); got.IP != want {
		t.Fatalf("клиент %v, ждали %v: петля [::1] не в API_TRUSTED_PROXIES из render.yaml (%q)",
			got.IP, want, stand["API_TRUSTED_PROXIES"])
	}
}
