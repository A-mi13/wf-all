package blob_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"wf/backend/internal/platform/blob"
)

func stores(t *testing.T) map[string]blob.Store {
	t.Helper()
	fs, err := blob.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]blob.Store{"fs": fs, "memory": blob.NewMemory()}
}

func TestStoreContract(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			key := "avatars/2026/10/a1b2.webp"
			if err := s.Put(ctx, key, strings.NewReader("image-bytes"), "image/webp"); err != nil {
				t.Fatal(err)
			}
			rc, info, err := s.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(rc)
			_ = rc.Close()
			if string(data) != "image-bytes" || info.Size != 11 || info.ContentType != "image/webp" {
				t.Fatalf("%q %+v", data, info)
			}
			// перезапись — новое содержимое целиком
			if err := s.Put(ctx, key, strings.NewReader("v2"), "image/png"); err != nil {
				t.Fatal(err)
			}
			rc, info, err = s.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			data, _ = io.ReadAll(rc)
			_ = rc.Close()
			if string(data) != "v2" || info.Size != 2 || info.ContentType != "image/png" {
				t.Fatalf("после перезаписи: %q %+v", data, info)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
				t.Fatalf("после удаления: %v", err)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatalf("повторное удаление: %v", err)
			}
			bads := []string{
				"", "/abs", "../escape", "a/../../b", "UPPER", "a b", "a\\b", strings.Repeat("a", 513),
				"a/./b", "a/", "a.", "a/.tmp-1", "a//b", "a..b", "a/b.", ".hidden", "a/.b", "a/-b", "a-/b",
			}
			for _, bad := range bads {
				if err := s.Put(ctx, bad, strings.NewReader("x"), "text/plain"); !errors.Is(err, blob.ErrBadKey) {
					t.Errorf("Put, ключ %q: %v", bad, err)
				}
				if _, _, err := s.Get(ctx, bad); !errors.Is(err, blob.ErrBadKey) {
					t.Errorf("Get, ключ %q: %v", bad, err)
				}
				if err := s.Delete(ctx, bad); !errors.Is(err, blob.ErrBadKey) {
					t.Errorf("Delete, ключ %q: %v", bad, err)
				}
			}
			// ровно 512 символов — допустимый ключ
			// (несколько сегментов: одно имя файла длиннее 255 символов не принимает файловая система)
			long := strings.Repeat(strings.Repeat("a", 100)+"/", 5) + "aaaaaaa"
			if len(long) != 512 {
				t.Fatalf("тест: длина ключа %d", len(long))
			}
			if err := s.Put(ctx, long, strings.NewReader("x"), "text/plain"); err != nil {
				t.Fatalf("ключ из 512 символов: %v", err)
			}
		})
	}
}

// Префикс существующего ключа — не файл: Get даёт ErrNotFound, Delete — идемпотентный успех,
// сам файл под префиксом остаётся.
func TestStorePrefixIsNotAFile(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			if err := s.Put(ctx, "avatars/x/f", strings.NewReader("data"), "text/plain"); err != nil {
				t.Fatal(err)
			}
			for _, prefix := range []string{"avatars", "avatars/x"} {
				if _, _, err := s.Get(ctx, prefix); !errors.Is(err, blob.ErrNotFound) {
					t.Errorf("Get(%q): %v", prefix, err)
				}
				if err := s.Delete(ctx, prefix); err != nil {
					t.Errorf("Delete(%q): %v", prefix, err)
				}
			}
			rc, _, err := s.Get(ctx, "avatars/x/f")
			if err != nil {
				t.Fatalf("файл под префиксом пропал: %v", err)
			}
			_ = rc.Close()
		})
	}
}
