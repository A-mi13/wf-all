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
	"strings"
	"syscall"
)

// FS — файлы в каталоге (dev, e2e). Объект — пара: данные в data/<ключ> и meta в meta/<ключ>@meta.json
// («@» в ключах не бывает — имя meta не совпадёт с каталогом другого ключа: «x» и «x.json/y» сосуществуют).
// Читаем объект, только когда записаны обе части и размер в meta равен размеру данных: Put сначала убирает старую
// meta, затем атомарно (временный файл и переименование) пишет данные и последней — meta; сбой посередине
// оставляет «нет объекта», а не смесь версий. Префикс существующего ключа («avatars» при «avatars/x/f») —
// каталог, не файл: Get даёт ErrNotFound, Delete ничего не делает, Put — ErrConflict.
// Get читает файл в память и закрывает его: на Windows открытый файл не даёт переименовать поверх него.
type FS struct {
	root string
	// fault — сбой между частями пары (только тесты).
	fault func() error
}

const metaSuffix = "@meta.json"

// fsMeta — содержимое файла meta.
type fsMeta struct {
	ContentType  string `json:"content_type"`
	CacheControl string `json:"cache_control,omitempty"`
	Size         int64  `json:"size"`
}

func NewFS(root string) (*FS, error) {
	for _, d := range []string{"data", "meta"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			return nil, fmt.Errorf("blob: %w", err)
		}
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

func (s *FS) dataPath(key string) string {
	return filepath.Join(s.root, "data", filepath.FromSlash(key))
}

func (s *FS) metaPath(key string) string {
	return filepath.Join(s.root, "meta", filepath.FromSlash(key)) + metaSuffix
}

// notExist — пути нет; ENOTDIR — часть пути оказалась файлом (Linux; Windows отвечает «путь не найден»).
func notExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func (s *FS) Put(_ context.Context, key string, r io.Reader, o PutOptions) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if err := s.checkConflict(key); err != nil {
		return err
	}
	// обе части — во временные файлы до любых изменений: ошибка чтения r ничего не трогает
	dataTmp, size, err := writeTemp(filepath.Dir(s.dataPath(key)), r)
	if err != nil {
		return s.conflictOr(key, err)
	}
	defer func() { _ = os.Remove(dataTmp) }() // после переименования — «нет файла», не ошибка
	meta, err := json.Marshal(fsMeta{ContentType: o.ContentType, CacheControl: o.CacheControl, Size: size})
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	metaTmp, _, err := writeTemp(filepath.Dir(s.metaPath(key)), bytes.NewReader(meta))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(metaTmp) }()
	// порядок пары: старая meta прочь → данные → новая meta (точка фиксации)
	if err := removeFile(s.metaPath(key)); err != nil {
		return err
	}
	if err := os.Rename(dataTmp, s.dataPath(key)); err != nil {
		return s.conflictOr(key, fmt.Errorf("blob: %w", err))
	}
	if s.fault != nil {
		if err := s.fault(); err != nil {
			return err
		}
	}
	if err := os.Rename(metaTmp, s.metaPath(key)); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// checkConflict — каталог на месте файла или файл на месте каталога-родителя: ErrConflict до записи.
func (s *FS) checkConflict(key string) error {
	segs := strings.Split(key, "/")
	for i := range segs {
		st, err := os.Lstat(s.dataPath(strings.Join(segs[:i+1], "/")))
		if notExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("blob: %w", err)
		}
		last := i == len(segs)-1
		if st.IsDir() == last {
			return ErrConflict
		}
	}
	return nil
}

// conflictOr — сбой записи из-за параллельного Put ключа-префикса (гонка с checkConflict) — ErrConflict.
func (s *FS) conflictOr(key string, err error) error {
	if errors.Is(s.checkConflict(key), ErrConflict) {
		return ErrConflict
	}
	return err
}

func (s *FS) Get(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	info, err := s.Head(ctx, key)
	if err != nil {
		return nil, Info{}, err
	}
	data, err := os.ReadFile(s.dataPath(key))
	if notExist(err) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, fmt.Errorf("blob: %w", err)
	}
	// между Head и чтением объект могли перезаписать: отдаём только согласованную пару
	if int64(len(data)) != info.Size {
		return nil, Info{}, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), info, nil
}

func (s *FS) Head(_ context.Context, key string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	raw, err := os.ReadFile(s.metaPath(key))
	if notExist(err) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, fmt.Errorf("blob: %w", err)
	}
	var m fsMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return Info{}, ErrNotFound
	}
	st, err := os.Lstat(s.dataPath(key))
	if notExist(err) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, fmt.Errorf("blob: %w", err)
	}
	if !st.Mode().IsRegular() || st.Size() != m.Size {
		return Info{}, ErrNotFound
	}
	return Info{Size: m.Size, ContentType: m.ContentType, CacheControl: m.CacheControl}, nil
}

// Delete — сначала meta (объект перестаёт читаться), затем данные.
func (s *FS) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	for _, p := range []string{s.metaPath(key), s.dataPath(key)} {
		if err := removeFile(p); err != nil {
			return err
		}
	}
	return nil
}

// List — ключи файлов данных (и без meta: их находит чистка); временные файлы не ключи.
func (s *FS) List(ctx context.Context, prefix string, fn func(key string) error) error {
	if err := ValidPrefix(prefix); err != nil {
		return err
	}
	data := filepath.Join(s.root, "data")
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

// writeTemp пишет r во временный файл в каталоге dir (та же ФС, что у назначения, — для Rename).
func writeTemp(dir string, r io.Reader) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, fmt.Errorf("blob: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", 0, fmt.Errorf("blob: %w", err)
	}
	n, err := io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", 0, fmt.Errorf("blob: %w", err)
	}
	return tmp.Name(), n, nil
}
