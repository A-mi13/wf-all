package blob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FS — файлы в каталоге (dev, e2e). Объект — один файл obj/<ключ>: данные, за ними meta (JSON: тип, Cache-Control,
// размер данных) и длина meta десятью цифрами. Put пишет всё во временный файл рядом и фиксирует одним
// переименованием: сбой до него оставляет прежнюю версию целиком (как у S3), параллельные Put одного ключа — версию
// последнего переименования, не смесь. Размер в meta = длина данных — защита от усечённого или испорченного
// снаружи файла: такой объект не читается (ErrNotFound). Префикс существующего ключа («avatars» при
// «avatars/x/f») — каталог, не файл: Get даёт ErrNotFound, Delete ничего не делает, Put — ErrConflict; пустой
// каталог (оборванный Put «a/b», удалены все «a/…») — не конфликт, Put «a» его убирает.
// Get читает файл в память и закрывает его: на Windows открытый файл не даёт переименовать поверх него.
type FS struct {
	root string
	// fault — сбой перед фиксацией, после записи временного файла (только тесты).
	fault func() error
}

// metaLenDigits — длина хвоста файла объекта: длина meta десятичными цифрами с ведущими нулями.
const metaLenDigits = 10

// errCorrupt — файл объекта не разбирается (усечён, испорчен, не наш формат): для читателя — ErrNotFound.
var errCorrupt = errors.New("blob: файл объекта испорчен")

// fsMeta — meta объекта в хвосте файла.
type fsMeta struct {
	ContentType  string `json:"content_type"`
	CacheControl string `json:"cache_control,omitempty"`
	Size         int64  `json:"size"`
}

func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(filepath.Join(root, "obj"), 0o750); err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	return &FS{root: root}, nil
}

// NewFSBuckets — бакеты-каталоги <root>/{in,pub,priv}.
func NewFSBuckets(root string) (Buckets, error) {
	var stores [3]*FS
	for i, name := range []string{"in", "pub", "priv"} {
		s, err := NewFS(filepath.Join(root, name))
		if err != nil {
			return Buckets{}, err
		}
		stores[i] = s
	}
	return Buckets{In: stores[0], Pub: stores[1], Priv: stores[2]}, nil
}

func (s *FS) path(key string) string {
	return filepath.Join(s.root, "obj", filepath.FromSlash(key))
}

// notExist — пути нет; ENOTDIR — часть пути оказалась файлом (Linux; Windows отвечает «путь не найден»).
func notExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func (s *FS) Put(_ context.Context, key string, r io.Reader, o PutOptions) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if err := s.checkConflict(key, true); err != nil {
		return err
	}
	// объект целиком — во временный файл до любых изменений: ошибка чтения r ничего не трогает
	tmp, err := writeTemp(filepath.Dir(s.path(key)), r, o)
	if err != nil {
		err = s.conflictOr(key, err)
		s.pruneDirs(key) // каталоги, созданные под оборванную запись
		return err
	}
	defer func() { _ = os.Remove(tmp) }() // после переименования — «нет файла», не ошибка
	if s.fault != nil {
		if err := s.fault(); err != nil {
			return err
		}
	}
	// точка фиксации: переименование поверх прежней версии атомарно
	if err := rename(tmp, s.path(key)); err != nil {
		return s.conflictOr(key, fmt.Errorf("blob: %w", err))
	}
	return nil
}

// checkConflict — каталог на месте файла или файл на месте каталога-родителя: ErrConflict до записи. prune — пустой
// каталог на месте ключа убрать и не считать конфликтом (syscall.Rmdir не удаляет непустой каталог и файл).
func (s *FS) checkConflict(key string, prune bool) error {
	segs := strings.Split(key, "/")
	for i := range segs {
		p := s.path(strings.Join(segs[:i+1], "/"))
		st, err := os.Lstat(p)
		if notExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("blob: %w", err)
		}
		last := i == len(segs)-1
		if last && st.IsDir() && prune && syscall.Rmdir(p) == nil {
			return nil
		}
		if st.IsDir() == last {
			return ErrConflict
		}
	}
	return nil
}

// conflictOr — сбой записи из-за параллельного Put ключа-префикса (гонка с checkConflict) — ErrConflict. Каталог
// здесь не убирается: его мог только что создать параллельный Put ключа под ним.
func (s *FS) conflictOr(key string, err error) error {
	if errors.Is(s.checkConflict(key, false), ErrConflict) {
		return ErrConflict
	}
	return err
}

func (s *FS) Get(_ context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	p := s.path(key)
	raw, err := os.ReadFile(p)
	if err != nil {
		if notExist(err) {
			return nil, Info{}, ErrNotFound
		}
		if st, serr := os.Lstat(p); serr == nil && !st.Mode().IsRegular() {
			return nil, Info{}, ErrNotFound
		}
		return nil, Info{}, fmt.Errorf("blob: %w", err)
	}
	m, err := readMeta(bytes.NewReader(raw), int64(len(raw)))
	if errors.Is(err, errCorrupt) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, err
	}
	return io.NopCloser(bytes.NewReader(raw[:m.Size])), m.info(), nil
}

func (s *FS) Head(_ context.Context, key string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	f, err := os.Open(s.path(key))
	if notExist(err) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, fmt.Errorf("blob: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Info{}, fmt.Errorf("blob: %w", err)
	}
	if !st.Mode().IsRegular() {
		return Info{}, ErrNotFound
	}
	m, err := readMeta(f, st.Size())
	if errors.Is(err, errCorrupt) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return m.info(), nil
}

func (m fsMeta) info() Info {
	return Info{Size: m.Size, ContentType: m.ContentType, CacheControl: m.CacheControl}
}

// readMeta — meta из хвоста файла объекта размера size; хвост не разбирается или размер в meta не равен длине
// данных — errCorrupt.
func readMeta(r io.ReaderAt, size int64) (fsMeta, error) {
	if size < metaLenDigits {
		return fsMeta{}, errCorrupt
	}
	var digits [metaLenDigits]byte
	if _, err := r.ReadAt(digits[:], size-metaLenDigits); err != nil {
		return fsMeta{}, fmt.Errorf("blob: %w", err)
	}
	n, err := strconv.ParseInt(string(digits[:]), 10, 64)
	if err != nil || n <= 0 || n > size-metaLenDigits {
		return fsMeta{}, errCorrupt
	}
	raw := make([]byte, n)
	if _, err := r.ReadAt(raw, size-metaLenDigits-n); err != nil {
		return fsMeta{}, fmt.Errorf("blob: %w", err)
	}
	var m fsMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return fsMeta{}, errCorrupt
	}
	if m.Size != size-metaLenDigits-n {
		return fsMeta{}, errCorrupt
	}
	return m, nil
}

// Delete удаляет файл объекта и опустевшие каталоги над ним; каталог-префикс и отсутствующий ключ — не ошибка.
func (s *FS) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if err := removeFile(s.path(key)); err != nil {
		return err
	}
	s.pruneDirs(key)
	return nil
}

// pruneDirs убирает опустевшие каталоги над ключом снизу вверх до корня obj: syscall.Rmdir не трогает ни непустой
// каталог, ни файл (ключ-префикс «a» при Delete «a/b»), поэтому объекты и временные файлы параллельного Put целы.
// Параллельный Put ключа под тем же каталогом, успевший создать каталог, но не временный файл, получит ошибку
// записи — повтор её снимает.
func (s *FS) pruneDirs(key string) {
	root := filepath.Join(s.root, "obj")
	for dir := filepath.Dir(s.path(key)); dir != root && strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
		if syscall.Rmdir(dir) != nil {
			return
		}
	}
}

// renameTries — попытки переименования на Windows (rename).
const renameTries = 10

// rename — os.Rename. Windows временно отказывает в переименовании поверх файла, который в этот миг открыт (Get,
// Head, параллельный Put того же ключа), и в каталог, который открыт проверкой конфликта, — повтор с паузой 1, 2, …
// мс (до ~55 мс); прочие ошибки и прочие ОС — сразу.
func rename(from, to string) error {
	var err error
	for i := range renameTries {
		if err = os.Rename(from, to); err == nil || !windowsBusy(err) {
			return err
		}
		time.Sleep(time.Duration(i+1) * time.Millisecond)
	}
	return err
}

// windowsBusy — ERROR_ACCESS_DENIED (5) или ERROR_SHARING_VIOLATION (32) на Windows: файл или каталог занят другим
// дескриптором.
func windowsBusy(err error) bool {
	var errno syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &errno) && (errno == 5 || errno == 32)
}

// List — ключи файлов объектов (и испорченных: их находит чистка); временные файлы не ключи.
func (s *FS) List(ctx context.Context, prefix string, fn func(key string) error) error {
	if err := ValidPrefix(prefix); err != nil {
		return err
	}
	data := filepath.Join(s.root, "obj")
	start := data
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		start = filepath.Join(data, filepath.FromSlash(prefix[:i]))
	}
	var keys []string
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == start && notExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(data, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, prefix) && ValidKey(key) == nil {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	return each(ctx, keys, fn)
}

// each зовёт fn по ключам вне замков и обходов (fn может удалять); отмена ctx — между вызовами.
func each(ctx context.Context, keys []string, fn func(string) error) error {
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(k); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// removeFile удаляет файл; несуществующий путь и каталог (префикс другого ключа) — не ошибка.
func removeFile(path string) error {
	st, err := os.Lstat(path)
	if notExist(err) || (err == nil && st.IsDir()) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := os.Remove(path); err != nil && !notExist(err) {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// writeTemp пишет объект (данные из r, meta с их размером, длина meta) во временный файл в каталоге dir (та же ФС,
// что у назначения, — для Rename).
func writeTemp(dir string, r io.Reader, o PutOptions) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("blob: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("blob: %w", err)
	}
	err = func() error {
		n, err := io.Copy(tmp, r)
		if err != nil {
			return err
		}
		meta, err := json.Marshal(fsMeta{ContentType: o.ContentType, CacheControl: o.CacheControl, Size: n})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(tmp, "%s%0*d", meta, metaLenDigits, len(meta))
		return err
	}()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("blob: %w", err)
	}
	return tmp.Name(), nil
}
