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
			_ = s.Put(ctx, key, strings.NewReader("v2"), "image/png")
			rc, info, _ = s.Get(ctx, key)
			data, _ = io.ReadAll(rc)
			_ = rc.Close()
			if string(data) != "v2" || info.ContentType != "image/png" {
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
			for _, bad := range []string{"", "/abs", "../escape", "a/../../b", "UPPER", "a b", "a\\b", strings.Repeat("a", 513)} {
				if err := s.Put(ctx, bad, strings.NewReader("x"), "text/plain"); !errors.Is(err, blob.ErrBadKey) {
					t.Errorf("ключ %q: %v", bad, err)
				}
			}
		})
	}
}
