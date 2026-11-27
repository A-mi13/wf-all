// Package blob — хранилище файлов (спека бэкенда §6.9, спека media §6): dev и e2e — файловая система
// (NewFSBuckets), тесты — память (NewMemoryBuckets), прод — S3-совместимое (пакет blob/s3). Модули видят только
// Store, Buckets и Presigner.
package blob

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotFound  = errors.New("blob: нет такого файла")
	ErrBadKey    = errors.New("blob: недопустимый ключ")
	ErrConflict  = errors.New("blob: ключ конфликтует с каталогом или файлом-префиксом")
	ErrSignature = errors.New("blob: подпись недействительна")
	ErrExpired   = errors.New("blob: срок подписи истёк")
)

// Info — размер и заголовки ответа объекта (их задаёт запись).
type Info struct {
	Size         int64
	ContentType  string
	CacheControl string
}

// PutOptions — заголовки, с которыми объект будет отдаваться.
type PutOptions struct {
	ContentType  string
	CacheControl string
}

type Store interface {
	Put(ctx context.Context, key string, r io.Reader, o PutOptions) error
	Get(ctx context.Context, key string) (io.ReadCloser, Info, error)
	Head(ctx context.Context, key string) (Info, error)
	// Delete — несуществующий файл не ошибка: повтор задачи удаления идемпотентен.
	Delete(ctx context.Context, key string) error
	// List зовёт fn для каждого ключа с префиксом prefix (строковый префикс, как у S3; "" — все ключи);
	// порядок не гарантирован. Ошибка fn останавливает обход и возвращается как есть.
	List(ctx context.Context, prefix string, fn func(key string) error) error
}

// Buckets — три бакета (спека §6.2): входящее (закрытое, жизненный цикл сутки), публичное, закрытое.
type Buckets struct{ In, Pub, Priv Store }

// UploadSpec — что разрешает подписанная загрузка (R3): ровно этот ключ бакета in, этот тип и ровно Bytes байт.
type UploadSpec struct {
	Key, ContentType string
	Bytes            int64
	TTL              time.Duration
}

// UploadForm — подписанный POST (R3): адрес и поля формы; файл — последним полем "file".
type UploadForm struct {
	URL       string
	Fields    map[string]string
	ExpiresAt time.Time
	MaxBytes  int64
}

type Presigner interface {
	// PresignUpload — загрузка в бакет in.
	PresignUpload(ctx context.Context, s UploadSpec) (UploadForm, error)
	// PresignGet — чтение из бакета priv.
	PresignGet(ctx context.Context, key string, ttl time.Duration, responseCacheControl string) (url string, expiresAt time.Time, err error)
}

// Opened — открытое хранилище процесса (фабрика Open — по конфигурации).
type Opened struct {
	Buckets   Buckets
	Presigner Presigner
	Purger    Purger
	// Dev — только fs: проверка подписи путей /_blob и /_media.
	Dev *DevSigner
	// PublicBaseURL — с завершающим "/".
	PublicBaseURL string
	// Available — false в режиме none.
	Available bool
}

// Каждый сегмент пути непустой, начинается и кончается на [a-z0-9]: так не бывает «.», завершающего
// «/», точки в конце сегмента (на Windows «dot.» совпадает с «dot») и сегментов «.tmp-…» —
// пространства имён временных файлов FS. Регулярка общая с S3 — не расширять.
var keyRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?(/[a-z0-9]([a-z0-9._-]*[a-z0-9])?)*$`)

// prefixRe — префикс List: алфавит ключа без требований к границам сегментов.
var prefixRe = regexp.MustCompile(`^[a-z0-9._/-]*$`)

const (
	// maxKeyLen — с запасом ниже предела Garage на lmdb: ключ записи LMDB ≤ 511 байт вместе с id бакета (32 байта),
	// длиннее — 503 MDB_BAD_VALSIZE. Ключи media — «<uuid>/<вариант>.webp», ~50 символов.
	maxKeyLen = 400
	// maxSegmentLen — запас до предела имени файла (255) под суффикс meta FS.
	maxSegmentLen = 200
)

// ValidKey — ключ одинаково безопасен для файловой системы любой ОС и S3: нижний регистр, цифры,
// «/ . _ -», без «..», пустых сегментов, ведущего или завершающего «/», сегментов длиннее 200 символов
// и имён устройств Windows.
func ValidKey(key string) error {
	if len(key) > maxKeyLen || !keyRe.MatchString(key) || strings.Contains(key, "..") {
		return ErrBadKey
	}
	for seg := range strings.SplitSeq(key, "/") {
		if len(seg) > maxSegmentLen || windowsDevice(seg) {
			return ErrBadKey
		}
	}
	return nil
}

// ValidPrefix — префикс List: пустой или из алфавита ключа, без «..», «//» и ведущего «/».
func ValidPrefix(prefix string) error {
	if len(prefix) > maxKeyLen || !prefixRe.MatchString(prefix) || strings.Contains(prefix, "..") ||
		strings.Contains(prefix, "//") || strings.HasPrefix(prefix, "/") {
		return ErrBadKey
	}
	return nil
}

// windowsDevice — имя устройства Windows (con, nul, aux, prn, com1…com9, lpt1…lpt9): Windows считает его
// устройством и с расширением, поэтому сравнивается часть до первой точки.
func windowsDevice(seg string) bool {
	base, _, _ := strings.Cut(seg, ".")
	switch base {
	case "con", "nul", "aux", "prn":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}
