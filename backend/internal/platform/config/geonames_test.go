package config_test

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wf/backend/internal/platform/config"
)

// WORKER_GEONAMES_CA_FILE (план B, Task 23): дополнительный корень для источника GeoNames — HTTPS-фикстура живого e2e
// и зеркала с собственным CA. На Windows Go не читает SSL_CERT_FILE (системное хранилище), поэтому корень — файлом.
func TestGeoNamesCAFile(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	// клиент с корнями из конфига — как его собирает воркер
	get := func(g config.GeoNames) error {
		pool, err := g.RootCAs()
		if err != nil {
			t.Fatal(err)
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		c := http.Client{Transport: tr}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	t.Run("пусто — системные корни", func(t *testing.T) {
		pool, err := config.GeoNames{}.RootCAs()
		if err != nil || pool != nil {
			t.Fatalf("pool = %v, err = %v; ждали nil, nil", pool, err)
		}
		// и сервер с тестовым корнем httptest им не доверен: проверка «годный» ниже не вакуумна
		if err := get(config.GeoNames{}); err == nil {
			t.Fatal("сервер httptest принят системными корнями")
		}
	})

	t.Run("годный — клиент доверяет серверу фикстуры", func(t *testing.T) {
		if err := get(config.GeoNames{CAFile: good}); err != nil {
			t.Fatalf("запрос к серверу с корнем из файла: %v", err)
		}
	})

	for _, tc := range []struct{ name, path string }{
		{"битый: файла нет", filepath.Join(dir, "nope.pem")},
		{"битый: не PEM", write("junk.pem", []byte("не сертификат"))},
		{"битый: PEM без сертификата", write("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}))},
		{"битый: испорченный сертификат", write("bad.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}}))},
		// испорченный рядом с годным не пропускается молча: оператор узнаёт о битом файле на старте
		{"битый: годный и испорченный в одном файле", write("mixed.pem", append(mustRead(t, good),
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}})...))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := config.GeoNames{CAFile: tc.path}.RootCAs()
			if err == nil || pool != nil {
				t.Fatalf("pool = %v, err = %v; ждали ошибку", pool, err)
			}
			if !strings.Contains(err.Error(), "WORKER_GEONAMES_CA_FILE") {
				t.Fatalf("ошибка не называет ключ: %v", err)
			}
		})
	}
}

func TestLoadWorkerGeoNamesCAFile(t *testing.T) {
	c, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x", "WORKER_GEONAMES_CA_FILE=/etc/wf/ca.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if c.GeoNames.CAFile != "/etc/wf/ca.pem" {
		t.Fatalf("CAFile = %q", c.GeoNames.CAFile)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // файл теста во временном каталоге
	if err != nil {
		t.Fatal(err)
	}
	return b
}
