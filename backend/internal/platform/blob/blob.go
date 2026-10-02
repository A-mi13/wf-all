// Package blob — хранилище файлов (спека бэкенда §6.9): dev — файловая система, прод —
// S3-совместимое (реализация — в спеке media). Модули видят только Store.
package blob

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
)

var (
	ErrNotFound = errors.New("blob: нет такого файла")
	ErrBadKey   = errors.New("blob: недопустимый ключ")
)

type Info struct {
	Size        int64
	ContentType string
}

type Store interface {
	Put(ctx context.Context, key string, r io.Reader, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, Info, error)
	// Delete — несуществующий файл не ошибка: повтор задачи удаления идемпотентен.
	Delete(ctx context.Context, key string) error
}

// Каждый сегмент пути непустой, начинается и кончается на [a-z0-9]: так не бывает «.», завершающего
// «/», точки в конце сегмента (на Windows «dot.» совпадает с «dot») и сегментов «.tmp-…» —
// пространства имён временных файлов FS.
var keyRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?(/[a-z0-9]([a-z0-9._-]*[a-z0-9])?)*$`)

const maxKeyLen = 512

// ValidKey — ключ одинаково безопасен для файловой системы и S3: нижний регистр, цифры,
// «/ . _ -», без «..», пустых сегментов и ведущего или завершающего «/».
func ValidKey(key string) error {
	if len(key) > maxKeyLen || !keyRe.MatchString(key) || strings.Contains(key, "..") {
		return ErrBadKey
	}
	return nil
}
