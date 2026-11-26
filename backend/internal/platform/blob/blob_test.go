package blob_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/blobtest"
)

func TestStoreContract(t *testing.T) {
	t.Run("fs", func(t *testing.T) {
		b, err := blob.NewFSBuckets(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		blobtest.Contract(t, b)
	})
	t.Run("memory", func(t *testing.T) { blobtest.Contract(t, blob.NewMemoryBuckets()) })
}

func TestNewFSBucketsLayout(t *testing.T) {
	root := t.TempDir()
	if _, err := blob.NewFSBuckets(root); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"in", "pub", "priv"} {
		if st, err := os.Stat(filepath.Join(root, d)); err != nil || !st.IsDir() {
			t.Errorf("нет каталога бакета %s: %v", d, err)
		}
	}
}

func newFS(t *testing.T) (*blob.FS, string) {
	t.Helper()
	root := t.TempDir()
	s, err := blob.NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

// files — все файлы и каталоги под root (относительные пути): «на диске ничего не изменилось».
func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func put(t *testing.T, s blob.Store, key, data, ct string) {
	t.Helper()
	if err := s.Put(context.Background(), key, strings.NewReader(data), blob.PutOptions{ContentType: ct}); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func read(t *testing.T, s blob.Store, key string) (string, blob.Info) {
	t.Helper()
	rc, info, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), info
}

func notFound(t *testing.T, s blob.Store, key string) {
	t.Helper()
	if _, _, err := s.Get(context.Background(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("Get(%q): %v, ждали ErrNotFound", key, err)
	}
	if _, err := s.Head(context.Background(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("Head(%q): %v, ждали ErrNotFound", key, err)
	}
}

func TestFSFoundationLeftovers(t *testing.T) {
	ctx := context.Background()

	t.Run("segment_len", func(t *testing.T) {
		s, root := newFS(t)
		ok := "a/" + strings.Repeat("b", 200)
		if err := blob.ValidKey(ok); err != nil {
			t.Fatalf("сегмент 200: %v", err)
		}
		put(t, s, ok, "x", "text/plain")
		before := files(t, root)
		for _, bad := range []string{"a/" + strings.Repeat("b", 201), strings.Repeat("c", 201) + "/d"} {
			if err := blob.ValidKey(bad); !errors.Is(err, blob.ErrBadKey) {
				t.Errorf("ValidKey(сегмент 201): %v", err)
			}
			if err := s.Put(ctx, bad, strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, blob.ErrBadKey) {
				t.Errorf("Put(сегмент 201): %v", err)
			}
		}
		if after := files(t, root); !slices.Equal(before, after) {
			t.Fatalf("частичная запись:\nбыло  %q\nстало %q", before, after)
		}
	})

	t.Run("enotdir", func(t *testing.T) {
		s, _ := newFS(t)
		put(t, s, "a", "file", "text/plain")
		notFound(t, s, "a/b")
		notFound(t, s, "a/b/c")
		if err := s.Delete(ctx, "a/b"); err != nil {
			t.Fatalf("Delete(a/b): %v", err)
		}
		if data, _ := read(t, s, "a"); data != "file" {
			t.Fatalf("файл a: %q", data)
		}
	})

	t.Run("put_over_dir", func(t *testing.T) {
		s, root := newFS(t)
		put(t, s, "a/x", "under", "text/plain")
		put(t, s, "b", "file", "text/plain")
		before := files(t, root)
		for _, key := range []string{"a", "b/c", "b/c/d"} {
			if err := s.Put(ctx, key, strings.NewReader("new"), blob.PutOptions{ContentType: "image/png"}); !errors.Is(err, blob.ErrConflict) {
				t.Errorf("Put(%q): %v, ждали ErrConflict", key, err)
			}
		}
		if after := files(t, root); !slices.Equal(before, after) {
			t.Fatalf("частичная запись:\nбыло  %q\nстало %q", before, after)
		}
		if data, info := read(t, s, "b"); data != "file" || info.ContentType != "text/plain" {
			t.Fatalf("файл b: %q %+v", data, info)
		}
		if data, _ := read(t, s, "a/x"); data != "under" {
			t.Fatalf("файл a/x: %q", data)
		}
	})

	t.Run("meta_collision", func(t *testing.T) {
		for _, order := range [][]string{{"x", "x.json/y"}, {"x.json/y", "x"}} {
			s, _ := newFS(t)
			put(t, s, order[0], "first", "text/plain")
			put(t, s, order[1], "second", "image/png")
			if data, info := read(t, s, order[0]); data != "first" || info.ContentType != "text/plain" {
				t.Fatalf("%q: %q %+v", order[0], data, info)
			}
			if data, info := read(t, s, order[1]); data != "second" || info.ContentType != "image/png" {
				t.Fatalf("%q: %q %+v", order[1], data, info)
			}
		}
	})

	t.Run("windows_names", func(t *testing.T) {
		s, _ := newFS(t)
		bads := []string{"con", "nul", "aux", "prn"}
		for d := '1'; d <= '9'; d++ {
			bads = append(bads, "com"+string(d), "lpt"+string(d))
		}
		for _, n := range bads {
			for _, key := range []string{n, n + ".webp", n + ".tar.gz", "a/" + n, "a/" + n + ".webp", n + "/b", "a/" + n + "/b"} {
				if err := blob.ValidKey(key); !errors.Is(err, blob.ErrBadKey) {
					t.Errorf("ValidKey(%q): %v", key, err)
				}
				if err := s.Put(ctx, key, strings.NewReader("x"), blob.PutOptions{}); !errors.Is(err, blob.ErrBadKey) {
					t.Errorf("Put(%q): %v", key, err)
				}
			}
		}
		// похожие, но не устройства
		for _, key := range []string{"com0", "lpt0", "com10", "conx", "console", "nul1", "a/xcon", "prn-1", "auxx.webp"} {
			if err := blob.ValidKey(key); err != nil {
				t.Errorf("ValidKey(%q): %v", key, err)
			}
			put(t, s, key, "x", "text/plain")
		}
	})

	t.Run("pair", func(t *testing.T) {
		s, _ := newFS(t)
		put(t, s, "obj", "data", "text/plain")
		if err := os.Remove(blob.FSMetaPath(s, "obj")); err != nil {
			t.Fatal(err)
		}
		notFound(t, s, "obj")
		// данные без meta — в List есть (их найдёт чистка), Delete убирает
		if err := s.Delete(ctx, "obj"); err != nil {
			t.Fatal(err)
		}
	})
}

// Сбой между частями пары: объект не читается — ни новый, ни смесь старой и новой версии.
func TestFSPairAtomic(t *testing.T) {
	ctx := context.Background()
	s, _ := newFS(t)
	boom := errors.New("сбой")
	blob.SetFSFault(s, func() error { return boom })

	if err := s.Put(ctx, "fresh", strings.NewReader("v1v1"), blob.PutOptions{ContentType: "text/plain"}); !errors.Is(err, boom) {
		t.Fatalf("Put со сбоем: %v", err)
	}
	notFound(t, s, "fresh")

	blob.SetFSFault(s, nil)
	put(t, s, "over", "v1v1", "text/plain")
	blob.SetFSFault(s, func() error { return boom })
	// та же длина: смесь «данные v1 + meta v2» не отличить по размеру
	err := s.Put(ctx, "over", strings.NewReader("v2v2"), blob.PutOptions{ContentType: "image/png", CacheControl: "no-store"})
	if !errors.Is(err, boom) {
		t.Fatalf("Put со сбоем: %v", err)
	}
	if rc, info, err := s.Get(ctx, "over"); err == nil {
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		t.Fatalf("после сбоя читается %q %+v", data, info)
	}
	notFound(t, s, "over")

	blob.SetFSFault(s, nil)
	put(t, s, "over", "v3", "image/webp")
	if data, info := read(t, s, "over"); data != "v3" || info.ContentType != "image/webp" || info.Size != 2 {
		t.Fatalf("после повтора: %q %+v", data, info)
	}
}

// Windows не переименовывает поверх открытого файла: незакрытый Get не мешает перезаписи и удалению.
func TestFSOverwriteWhileReading(t *testing.T) {
	ctx := context.Background()
	s, _ := newFS(t)
	put(t, s, "k", "old", "text/plain")
	rc, _, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	put(t, s, "k", "new", "text/plain")
	if data, _ := read(t, s, "k"); data != "new" {
		t.Fatalf("после перезаписи: %q", data)
	}
	if old, _ := io.ReadAll(rc); string(old) != "old" {
		t.Fatalf("открытый читатель: %q", old)
	}
	rc2, _, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc2.Close() }()
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete при открытом читателе: %v", err)
	}
	notFound(t, s, "k")
}

// Put «a» ∥ Put «a/x»: ровно один успех, проигравший ничего не оставил читаемым.
func TestFSConcurrentConflict(t *testing.T) {
	ctx := context.Background()
	for range 20 {
		s, _ := newFS(t)
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := map[string]error{}
		var mu sync.Mutex
		for _, key := range []string{"a", "a/x"} {
			wg.Go(func() {
				<-start
				err := s.Put(ctx, key, strings.NewReader(key), blob.PutOptions{ContentType: "text/plain"})
				mu.Lock()
				errs[key] = err
				mu.Unlock()
			})
		}
		close(start)
		wg.Wait()
		if (errs["a"] == nil) == (errs["a/x"] == nil) {
			t.Fatalf("ждали ровно один успех: %v", errs)
		}
		for key, err := range errs {
			if err == nil {
				if data, _ := read(t, s, key); data != key {
					t.Fatalf("%q: %q", key, data)
				}
			} else {
				notFound(t, s, key)
			}
		}
	}
}

func TestListContext(t *testing.T) {
	stores := map[string]blob.Store{"memory": blob.NewMemory()}
	fsStore, _ := newFS(t)
	stores["fs"] = fsStore
	for name, s := range stores {
		t.Run(name, func(t *testing.T) {
			put(t, s, "p/a", "x", "")
			put(t, s, "p/b", "x", "")
			ctx, cancel := context.WithCancel(context.Background())
			calls := 0
			err := s.List(ctx, "p/", func(string) error { calls++; cancel(); return nil })
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("List после отмены: %v, вызовов %d", err, calls)
			}
		})
	}
}

func TestNoopPurger(t *testing.T) {
	var p blob.Purger = blob.NoopPurger{}
	if err := p.Purge(context.Background(), []string{"https://media.example/x.webp"}); err != nil {
		t.Fatal(err)
	}
}
