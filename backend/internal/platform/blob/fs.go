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
)

// FS — файлы в каталоге (dev): данные в data/<ключ>, тип — в meta/<ключ>.json. Запись атомарна:
// временный файл и переименование.
type FS struct{ root string }

func NewFS(root string) (*FS, error) {
	for _, d := range []string{"data", "meta"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			return nil, fmt.Errorf("blob: %w", err)
		}
	}
	return &FS{root: root}, nil
}

func (s *FS) path(kind, key string) string {
	return filepath.Join(s.root, kind, filepath.FromSlash(key))
}

func (s *FS) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	meta, err := json.Marshal(Info{ContentType: contentType})
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path("data", key), r); err != nil {
		return err
	}
	return writeAtomic(s.path("meta", key)+".json", bytes.NewReader(meta))
}

func (s *FS) Get(_ context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	f, err := os.Open(s.path("data", key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, fmt.Errorf("blob: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Info{}, err
	}
	var info Info
	if raw, err := os.ReadFile(s.path("meta", key) + ".json"); err == nil {
		_ = json.Unmarshal(raw, &info)
	}
	info.Size = st.Size()
	return f, info, nil
}

func (s *FS) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	for _, p := range []string{s.path("data", key), s.path("meta", key) + ".json"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("blob: %w", err)
		}
	}
	return nil
}

func writeAtomic(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}
