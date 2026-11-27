package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Memory — хранилище в памяти: тесты модулей. Ключи — как у S3: «a» и «a/b» сосуществуют.
type Memory struct {
	mu    sync.Mutex
	files map[string]memFile
}

type memFile struct {
	data []byte
	info Info
}

func NewMemory() *Memory { return &Memory{files: map[string]memFile{}} }

// NewMemoryBuckets — три независимых бакета в памяти.
func NewMemoryBuckets() Buckets {
	return Buckets{In: NewMemory(), Pub: NewMemory(), Priv: NewMemory()}
}

func (m *Memory) Put(_ context.Context, key string, r io.Reader, o PutOptions) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[key] = memFile{data: data, info: Info{Size: int64(len(data)), ContentType: o.ContentType, CacheControl: o.CacheControl}}
	return nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, Info, error) {
	f, err := m.file(key)
	if err != nil {
		return nil, Info{}, err
	}
	return io.NopCloser(bytes.NewReader(f.data)), f.info, nil
}

func (m *Memory) Head(_ context.Context, key string) (Info, error) {
	f, err := m.file(key)
	return f.info, err
}

func (m *Memory) file(key string) (memFile, error) {
	if err := ValidKey(key); err != nil {
		return memFile{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[key]
	if !ok {
		return memFile{}, ErrNotFound
	}
	return f, nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}

// List — снимок ключей под замком, колбэк — без замка (может удалять).
func (m *Memory) List(ctx context.Context, prefix string, fn func(key string) error) error {
	if err := ValidPrefix(prefix); err != nil {
		return err
	}
	m.mu.Lock()
	var keys []string
	for k := range m.files {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	m.mu.Unlock()
	return each(ctx, keys, fn)
}
