package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/platform/config"
)

var testdata = filepath.Join("..", "..", "internal", "geo", "internal", "source", "testdata")

// Фикстура отдаёт выгрузку RU в форме, которую читает source.Fetch (пакет internal geo — из cmd не импортируется;
// сам Fetch по фикстуре проходит живой e2e): admin1 как есть, архивы — ровно запись RU.txt и readme.txt с телом
// выдержки; корню из --ca-out клиент доверяет так же, как воркер (config.GeoNames.RootCAs). Незнакомая страна — 404.
func TestFixtureServesDump(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"--data", testdata, "--ca-out", ca}, pw)
		_ = pw.Close()
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	base, ok := strings.CutPrefix(strings.TrimSpace(line), "geofixture: ")
	if !ok || !strings.HasPrefix(base, "https://127.0.0.1:") {
		t.Fatalf("строка готовности: %q", line)
	}

	pool, err := config.GeoNames{CAFile: ca}.RootCAs()
	if err != nil {
		t.Fatal(err)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	c := &http.Client{Transport: tr}
	get := func(path string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, b
	}
	want := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(testdata, name)) //nolint:gosec // выдержки GeoNames репо
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	if code, _ := get("/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz: %d", code)
	}
	if code, b := get("/export/dump/admin1CodesASCII.txt"); code != http.StatusOK || !bytes.Equal(b, want("admin1CodesASCII.txt")) {
		t.Fatalf("admin1: %d, %d байт", code, len(b))
	}
	for path, src := range map[string]string{
		"/export/dump/RU.zip":                "RU.txt",
		"/export/dump/alternatenames/RU.zip": "alternateNamesV2-RU.txt",
	} {
		code, b := get(path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var names []string
		var body []byte
		for _, f := range zr.File {
			names = append(names, f.Name)
			if f.Name == "RU.txt" {
				rc, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				body, _ = io.ReadAll(rc)
				_ = rc.Close()
			}
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{"RU.txt", "readme.txt"}) {
			t.Fatalf("%s: записи %v", path, names)
		}
		if !bytes.Equal(body, want(src)) {
			t.Fatalf("%s: тело RU.txt не равно %s", path, src)
		}
	}
	for _, path := range []string{"/export/dump/KZ.zip", "/export/dump/alternatenames/KZ.zip", "/export/dump/RU.txt"} {
		if code, _ := get(path); code != http.StatusNotFound {
			t.Fatalf("%s: %d, ждали 404", path, code)
		}
	}
}

func TestFixtureArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--ca-out", "x.pem"},
		{"--data", testdata},
		{"--data", testdata, "--ca-out", "x.pem", "лишнее"},
		{"--data", t.TempDir(), "--ca-out", filepath.Join(t.TempDir(), "x.pem")},
	} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Errorf("%q: принято", args)
		}
	}
}
