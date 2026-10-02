package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
)

// Memory — хранилище в памяти: тесты модулей.
type Memory struct {
	mu    sync.Mutex
	files map[string]memFile
}

type memFile struct {
	data []byte
	info Info
}

func NewMemory() *Memory { return &Memory{files: map[string]memFile{}} }

func (m *Memory) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[key] = memFile{data: data, info: Info{Size: int64(len(data)), ContentType: contentType}}
	return nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[key]
	if !ok {
		return nil, Info{}, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(f.data)), f.info, nil
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
