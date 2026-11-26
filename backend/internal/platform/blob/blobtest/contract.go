// Package blobtest — общий контрактный набор хранилища: его проходят FS, Memory и S3 (blob/s3).
package blobtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/platform/blob"
)

// Contract проверяет бакеты b. Ключи — под случайным префиксом прогона: бакеты S3 переживают прогоны.
func Contract(t *testing.T, b blob.Buckets) {
	t.Helper()
	ctx := context.Background()
	run := runPrefix(t)
	k := func(s string) string { return run + "/" + s }

	t.Run("put_get_head", func(t *testing.T) {
		s := b.Pub
		key := k("avatars/2026/10/a1b2.webp")
		opts := blob.PutOptions{ContentType: "image/webp", CacheControl: "public, max-age=31536000, immutable"}
		put(t, s, key, "image-bytes", opts)
		expect(t, s, key, "image-bytes", blob.Info{Size: 11, ContentType: opts.ContentType, CacheControl: opts.CacheControl})

		// перезапись — новое содержимое и заголовки целиком
		put(t, s, key, "v2", blob.PutOptions{ContentType: "image/png"})
		expect(t, s, key, "v2", blob.Info{Size: 2, ContentType: "image/png"})

		if err := s.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("Get после удаления: %v", err)
		}
		if _, err := s.Head(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("Head после удаления: %v", err)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatalf("повторное удаление: %v", err)
		}
	})

	t.Run("empty_object", func(t *testing.T) {
		key := k("empty")
		put(t, b.Priv, key, "", blob.PutOptions{ContentType: "application/octet-stream"})
		expect(t, b.Priv, key, "", blob.Info{Size: 0, ContentType: "application/octet-stream"})
		cleanup(t, b.Priv, key)
	})

	t.Run("delete_missing", func(t *testing.T) {
		if err := b.In.Delete(ctx, k("never-was")); err != nil {
			t.Fatal(err)
		}
		if _, err := b.In.Head(ctx, k("never-was")); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("Head: %v", err)
		}
	})

	t.Run("bad_keys", func(t *testing.T) {
		bads := []string{
			"", "/abs", "../escape", "a/../../b", "UPPER", "a b", "a\\b", strings.Repeat("a", 513),
			"a/./b", "a/", "a.", "a/.tmp-1", "a//b", "a..b", "a/b.", ".hidden", "a/.b", "a/-b", "a-/b",
			"ключ", "a/" + strings.Repeat("b", 201), "con", "a/nul.webp", "com1/x", "lpt9.tar.gz",
		}
		for _, bad := range bads {
			if err := b.Pub.Put(ctx, bad, strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, blob.ErrBadKey) {
				t.Errorf("Put, ключ %q: %v", bad, err)
			}
			if _, _, err := b.Pub.Get(ctx, bad); !errors.Is(err, blob.ErrBadKey) {
				t.Errorf("Get, ключ %q: %v", bad, err)
			}
			if _, err := b.Pub.Head(ctx, bad); !errors.Is(err, blob.ErrBadKey) {
				t.Errorf("Head, ключ %q: %v", bad, err)
			}
			if err := b.Pub.Delete(ctx, bad); !errors.Is(err, blob.ErrBadKey) {
				t.Errorf("Delete, ключ %q: %v", bad, err)
			}
		}
	})

	t.Run("max_key", func(t *testing.T) {
		// ровно 512 символов (сегменты по 100: имя файла длиннее 255 не примет файловая система)
		long := run + "/" + strings.Repeat(strings.Repeat("a", 100)+"/", 4)
		long += strings.Repeat("a", 512-len(long))
		if len(long) != 512 {
			t.Fatalf("тест: длина ключа %d", len(long))
		}
		put(t, b.Pub, long, "x", blob.PutOptions{ContentType: "text/plain"})
		cleanup(t, b.Pub, long)
	})

	t.Run("prefix_is_not_a_file", func(t *testing.T) {
		s := b.Pub
		put(t, s, k("dir/x/f"), "data", blob.PutOptions{ContentType: "text/plain"})
		cleanup(t, s, k("dir/x/f"))
		for _, prefix := range []string{k("dir"), k("dir/x")} {
			if _, _, err := s.Get(ctx, prefix); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("Get(%q): %v", prefix, err)
			}
			if _, err := s.Head(ctx, prefix); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("Head(%q): %v", prefix, err)
			}
			if err := s.Delete(ctx, prefix); err != nil {
				t.Errorf("Delete(%q): %v", prefix, err)
			}
		}
		expect(t, s, k("dir/x/f"), "data", blob.Info{Size: 4, ContentType: "text/plain"})
	})

	t.Run("list", func(t *testing.T) {
		s := b.Priv
		keys := []string{k("obj1/a.webp"), k("obj1/b.webp"), k("obj10/a.webp"), k("obj2/a.webp")}
		for _, key := range keys {
			put(t, s, key, "x", blob.PutOptions{ContentType: "image/webp"})
		}
		cleanup(t, s, keys...)
		if got, want := list(t, s, k("obj1/")), keys[:2]; !slices.Equal(got, want) {
			t.Fatalf("List(obj1/) = %q, ждали %q", got, want)
		}
		if got := list(t, s, k("obj1")); len(got) != 3 {
			t.Fatalf("List(obj1) — строковый префикс, ждали 3 ключа: %q", got)
		}
		if got := list(t, s, k("none/")); len(got) != 0 {
			t.Fatalf("List(none/) = %q", got)
		}
	})

	t.Run("list_error_stops", func(t *testing.T) {
		s := b.Priv
		keys := []string{k("stop/a"), k("stop/b"), k("stop/c")}
		for _, key := range keys {
			put(t, s, key, "x", blob.PutOptions{})
		}
		cleanup(t, s, keys...)
		stop := errors.New("стоп")
		calls := 0
		err := s.List(ctx, k("stop/"), func(string) error { calls++; return stop })
		if !errors.Is(err, stop) || calls != 1 {
			t.Fatalf("ошибка колбэка: %v, вызовов %d", err, calls)
		}
	})

	t.Run("list_delete_in_callback", func(t *testing.T) {
		s := b.In
		keys := []string{k("gc/a"), k("gc/b")}
		for _, key := range keys {
			put(t, s, key, "x", blob.PutOptions{})
		}
		if err := s.List(ctx, k("gc/"), func(key string) error { return s.Delete(ctx, key) }); err != nil {
			t.Fatal(err)
		}
		if got := list(t, s, k("gc/")); len(got) != 0 {
			t.Fatalf("после чистки: %q", got)
		}
	})

	t.Run("list_bad_prefix", func(t *testing.T) {
		for _, bad := range []string{"../", "/a", "a\\b", "a//b", "A"} {
			err := b.Pub.List(ctx, bad, func(string) error { return nil })
			if !errors.Is(err, blob.ErrBadKey) {
				t.Errorf("List(%q): %v", bad, err)
			}
		}
	})

	t.Run("buckets_isolated", func(t *testing.T) {
		key := k("iso/obj")
		put(t, b.In, key, "in", blob.PutOptions{ContentType: "text/plain"})
		cleanup(t, b.In, key)
		for name, s := range map[string]blob.Store{"pub": b.Pub, "priv": b.Priv} {
			if _, _, err := s.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("%s.Get: %v", name, err)
			}
			if _, err := s.Head(ctx, key); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("%s.Head: %v", name, err)
			}
			if got := list(t, s, k("iso/")); len(got) != 0 {
				t.Errorf("%s.List: %q", name, got)
			}
		}
		if err := b.Pub.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		expect(t, b.In, key, "in", blob.Info{Size: 2, ContentType: "text/plain"})
	})
}

func runPrefix(t *testing.T) string {
	t.Helper()
	var r [6]byte
	if _, err := rand.Read(r[:]); err != nil {
		t.Fatal(err)
	}
	return "t" + hex.EncodeToString(r[:])
}

func put(t *testing.T, s blob.Store, key, data string, o blob.PutOptions) {
	t.Helper()
	if err := s.Put(context.Background(), key, strings.NewReader(data), o); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func cleanup(t *testing.T, s blob.Store, keys ...string) {
	t.Cleanup(func() {
		for _, key := range keys {
			if err := s.Delete(context.Background(), key); err != nil {
				t.Errorf("уборка %q: %v", key, err)
			}
		}
	})
}

// expect — Get и Head отдают ровно эти байты и Info.
func expect(t *testing.T, s blob.Store, key, data string, want blob.Info) {
	t.Helper()
	rc, info, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data || info != want {
		t.Fatalf("Get(%q) = %q %+v, ждали %q %+v", key, got, info, data, want)
	}
	head, err := s.Head(context.Background(), key)
	if err != nil {
		t.Fatalf("Head(%q): %v", key, err)
	}
	if head != want {
		t.Fatalf("Head(%q) = %+v, ждали %+v", key, head, want)
	}
}

// list — ключи с префиксом по возрастанию (порядок List не гарантирован).
func list(t *testing.T, s blob.Store, prefix string) []string {
	t.Helper()
	var keys []string
	if err := s.List(context.Background(), prefix, func(key string) error {
		keys = append(keys, key)
		return nil
	}); err != nil {
		t.Fatalf("List(%q): %v", prefix, err)
	}
	slices.Sort(keys)
	return keys
}
