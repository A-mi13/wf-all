package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wf/backend/internal/platform/perm"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // временный файл теста
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Таблица между маркерами заменяется целиком, текст вокруг остаётся; каталог пишется; повторный
// запуск ничего не меняет.
func TestRunRewritesTableAndCatalog(t *testing.T) {
	dir := t.TempDir()
	doc, js := filepath.Join(dir, "05.md"), filepath.Join(dir, "permissions.json")
	src := "# Модель\n\nтекст до\n\n" + perm.DocBegin + "\nустаревшая таблица\n" + perm.DocEnd + "\n\nтекст после\n"
	if err := os.WriteFile(doc, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "# Модель\n\nтекст до\n\n" + perm.DocBegin + "\n" + perm.Markdown() + perm.DocEnd + "\n\nтекст после\n"
	wantJSON, err := perm.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := run(doc, js); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, doc); got != want {
			t.Fatalf("документ:\n%s\nнужно:\n%s", got, want)
		}
		if got := readFile(t, js); got != string(wantJSON) {
			t.Fatalf("каталог:\n%s\nнужно:\n%s", got, wantJSON)
		}
	}
}

// Документ без маркеров — ошибка; ни документ, ни каталог не трогаются.
func TestRunRefusesDocWithoutMarkers(t *testing.T) {
	dir := t.TempDir()
	doc, js := filepath.Join(dir, "05.md"), filepath.Join(dir, "permissions.json")
	const src = "# Модель без маркеров\n"
	if err := os.WriteFile(doc, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(doc, js); !errors.Is(err, perm.ErrMarkers) {
		t.Fatalf("err = %v, нужен perm.ErrMarkers", err)
	}
	if got := readFile(t, doc); got != src {
		t.Fatalf("документ изменён при ошибке: %q", got)
	}
	if _, err := os.Stat(js); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("каталог записан при ошибке документа: %v", err)
	}
}
