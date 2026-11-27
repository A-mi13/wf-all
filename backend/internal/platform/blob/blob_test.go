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
	"sync/atomic"
	"testing"
	"time"

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

	// Размер в meta = длина данных (ревью Task 2, m4): усечённый, дописанный или испорченный файл объекта не
	// читается; в List он есть (его найдёт чистка), Delete убирает.
	t.Run("size_mismatch", func(t *testing.T) {
		s, _ := newFS(t)
		put(t, s, "obj", "data", "text/plain")
		path := blob.FSObjectPath(s, "obj")
		good, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		tail := len(good) - len("data")
		for name, bad := range map[string][]byte{
			// данные на байт длиннее и короче, хвост meta цел — расходится только размер
			"longer":  append([]byte("datax"), good[len("data"):]...),
			"shorter": append([]byte("dat"), good[len("data"):]...),
			"cut":     good[:len(good)-1],
			"no_meta": []byte("data"),
			"empty":   {},
		} {
			if err := os.WriteFile(path, bad, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Get(ctx, "obj"); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("%s: Get: %v, ждали ErrNotFound", name, err)
			}
			if _, err := s.Head(ctx, "obj"); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("%s: Head: %v, ждали ErrNotFound", name, err)
			}
		}
		if tail <= 0 {
			t.Fatalf("у файла объекта нет хвоста meta: %q", good)
		}
		var keys []string
		if err := s.List(ctx, "", func(k string) error { keys = append(keys, k); return nil }); err != nil ||
			!slices.Equal(keys, []string{"obj"}) {
			t.Fatalf("List: %q %v", keys, err)
		}
		if err := s.Delete(ctx, "obj"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("после Delete файл остался: %v", err)
		}
	})
}

// Сбой Put до фиксации (ревью Task 2, m1): как у S3 — новый ключ не появляется, прежняя версия читается целиком
// (данные, тип, Cache-Control), на диске не остаётся временных файлов.
func TestFSFailedPutKeepsOld(t *testing.T) {
	ctx := context.Background()
	s, root := newFS(t)
	put(t, s, "over", "v1v1", "text/plain")
	before := files(t, root)
	boom := errors.New("сбой")
	blob.SetFSFault(s, func() error { return boom })

	if err := s.Put(ctx, "fresh", strings.NewReader("v1v1"), blob.PutOptions{ContentType: "text/plain"}); !errors.Is(err, boom) {
		t.Fatalf("Put со сбоем: %v", err)
	}
	notFound(t, s, "fresh")
	// та же длина и другая: ни смеси версий, ни потери старой
	for _, v := range []string{"v2v2", "v2-longer"} {
		err := s.Put(ctx, "over", strings.NewReader(v), blob.PutOptions{ContentType: "image/png", CacheControl: "no-store"})
		if !errors.Is(err, boom) {
			t.Fatalf("Put со сбоем: %v", err)
		}
		if data, info := read(t, s, "over"); data != "v1v1" || info.ContentType != "text/plain" || info.CacheControl != "" ||
			info.Size != 4 {
			t.Fatalf("после сбоя Put(%q): %q %+v, ждали прежнюю версию", v, data, info)
		}
	}
	if after := files(t, root); !slices.Equal(before, after) {
		t.Fatalf("после сбоев на диске осталось лишнее:\nбыло  %q\nстало %q", before, after)
	}

	blob.SetFSFault(s, nil)
	put(t, s, "over", "v3", "image/webp")
	if data, info := read(t, s, "over"); data != "v3" || info.ContentType != "image/webp" || info.CacheControl != "" ||
		info.Size != 2 {
		t.Fatalf("после повтора: %q %+v", data, info)
	}
}

// Два Put одного ключа разной длины (ревью Task 2, m3): оба успешны — читается ровно одна из версий целиком, а не
// «нет объекта» и не смесь. Детерминированно: второй Put целиком проходит, пока первый стоит перед фиксацией.
func TestFSSameKeyRace(t *testing.T) {
	ctx := context.Background()
	s, _ := newFS(t)
	fired := false
	blob.SetFSFault(s, func() error {
		if !fired {
			fired = true
			put(t, s, "k", "second-longer", "image/png")
		}
		return nil
	})
	put(t, s, "k", "first", "text/plain")
	if !fired {
		t.Fatal("крючок перед фиксацией не вызван")
	}
	if data, info := read(t, s, "k"); data != "first" || info.ContentType != "text/plain" || info.Size != 5 {
		t.Fatalf("после гонки: %q %+v, ждали версию последней фиксации", data, info)
	}

	// то же без крючка — общий старт двух горутин
	for range 20 {
		s, _ := newFS(t)
		start := make(chan struct{})
		var wg sync.WaitGroup
		versions := map[string]string{"short": "text/plain", "much-longer-value": "image/png"}
		for v, ct := range versions {
			wg.Go(func() {
				<-start
				if err := s.Put(ctx, "k", strings.NewReader(v), blob.PutOptions{ContentType: ct}); err != nil {
					t.Errorf("Put(%q): %v", v, err)
				}
			})
		}
		close(start)
		wg.Wait()
		if data, info := read(t, s, "k"); versions[data] != info.ContentType || info.Size != int64(len(data)) {
			t.Fatalf("после гонки: %q %+v", data, info)
		}
	}
}

// Пустые каталоги (ревью Task 2, m2): после оборванного Put «a/b» и после удаления всех «x/…» ключ «a» («x»)
// записывается — пустой каталог не конфликт. Каталог с объектами — по-прежнему ErrConflict (put_over_dir).
func TestFSEmptyDirsNoConflict(t *testing.T) {
	ctx := context.Background()
	s, root := newFS(t)
	before := files(t, root)
	broken := io.MultiReader(strings.NewReader("part"), brokenBody{})
	if err := s.Put(ctx, "a/b/c", broken, blob.PutOptions{}); err == nil {
		t.Fatal("Put с обрывом тела: ждали ошибку")
	}
	notFound(t, s, "a/b/c")
	if after := files(t, root); !slices.Equal(before, after) {
		t.Fatalf("оборванный Put оставил каталоги:\nбыло  %q\nстало %q", before, after)
	}
	put(t, s, "a", "file", "text/plain")
	if data, _ := read(t, s, "a"); data != "file" {
		t.Fatalf("a: %q", data)
	}

	put(t, s, "x/y/z", "deep", "text/plain")
	if err := s.Delete(ctx, "x/y/z"); err != nil {
		t.Fatal(err)
	}
	if after := files(t, root); !slices.Contains(after, "obj/a") || slices.Contains(after, "obj/x/") {
		t.Fatalf("после Delete остались пустые каталоги: %q", after)
	}
	put(t, s, "x", "file", "text/plain")
	if data, _ := read(t, s, "x"); data != "file" {
		t.Fatalf("x: %q", data)
	}

	// пустой каталог, оставленный падением процесса (или раскладкой до правки), — не конфликт
	if err := os.MkdirAll(blob.FSObjectPath(s, "e"), 0o750); err != nil {
		t.Fatal(err)
	}
	put(t, s, "e", "file", "text/plain")
	if data, _ := read(t, s, "e"); data != "file" {
		t.Fatalf("e: %q", data)
	}
}

// Гонка с уборкой пустого каталога (ревью R2, m3): Put «a/x» создал каталог «a», и до его временного файла Put «a»
// убрал этот каталог как пустой. Ровно один успех, проигравший — ErrConflict, а не «нет такого файла».
// Детерминированно: Put «a» запускается из крючка Put «a/x» между каталогом и временным файлом и стоит перед своей
// фиксацией, пока «a/x» не закончит.
func TestFSPruneRaceConflict(t *testing.T) {
	ctx := context.Background()
	s, _ := newFS(t)
	// stall — свой срок ожидания: поломка, при которой Put «a» не доходит до фиксации или не возвращается, роняет
	// тест сразу, а не висит до -timeout пакета
	const stall = 10 * time.Second
	reached, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock) // после t.Fatal Put «a» не остаётся висеть на release
	var faults atomic.Int32
	blob.SetFSFault(s, func() error {
		if faults.Add(1) == 1 { // первый до фиксации — Put «a»: ждёт, пока «a/x» закончит
			close(reached)
			<-release
		}
		return nil
	})
	var first atomic.Bool // не sync.Once: Put «a» зовёт крючок, пока первый вызов ещё ждёт
	errA := make(chan error, 1)
	blob.SetFSBeforeCreate(s, func() {
		if first.Swap(true) {
			return
		}
		go func() {
			errA <- s.Put(ctx, "a", strings.NewReader("file"), blob.PutOptions{ContentType: "text/plain"})
		}()
		// Put «a» убрал пустой «a» и стоит перед фиксацией
		select {
		case <-reached:
		case err := <-errA:
			t.Fatalf("Put «a» вернулся (%v), не дойдя до точки фиксации — гонку не воспроизвести", err)
		case <-time.After(stall):
			t.Fatalf("Put «a» за %s не дошёл до точки фиксации", stall)
		}
	})
	errAX := s.Put(ctx, "a/x", strings.NewReader("under"), blob.PutOptions{ContentType: "text/plain"})
	unblock()
	var gotA error
	select {
	case gotA = <-errA:
	case <-time.After(stall):
		t.Fatalf("Put «a» не вернулся за %s после фиксации «a/x»", stall)
	}
	errs := map[string]error{"a": gotA, "a/x": errAX}
	if (errs["a"] == nil) == (errs["a/x"] == nil) {
		t.Fatalf("ждали ровно один успех: %v", errs)
	}
	for key, err := range errs {
		if err == nil {
			if data, _ := read(t, s, key); data != map[string]string{"a": "file", "a/x": "under"}[key] {
				t.Fatalf("%q: %q", key, data)
			}
			continue
		}
		if !errors.Is(err, blob.ErrConflict) {
			t.Errorf("проигравший %q: %v, ждали ErrConflict", key, err)
		}
		notFound(t, s, key)
	}
}

// brokenBody — обрыв тела загрузки.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("обрыв") }

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
