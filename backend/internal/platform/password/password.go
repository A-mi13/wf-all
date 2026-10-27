// Package password — хэширование паролей (спека бэкенда §6.9, §7.1): argon2id в формате PHC,
// параметры не ниже OWASP (m = 19 МиБ, t = 2, p = 1) и не выше потолков (m = 256 МиБ, t = 10,
// p = 16), пересчёт хэша при входе, если параметры выросли; число одновременных хэширований
// ограничено семафором. Пароль на входе — не длиннее MaxInputBytes и приводится к NFKC: одна и та
// же строка, набранная составными или полноширинными символами, даёт один хэш.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

type Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
}

// Минимум OWASP для argon2id и потолки: хэш из базы с параметрами выше потолка не проверяется
// (подменённая строка с m = 4 ГиБ положила бы процесс), соль короче 16 байт — тоже.
const (
	MinMemoryKiB   uint32 = 19456
	MinIterations  uint32 = 2
	MinParallelism uint8  = 1

	MaxMemoryKiB   uint32 = 256 * 1024
	MaxIterations  uint32 = 10
	MaxParallelism uint8  = 16

	// MaxInputBytes — потолок пароля на входе в байтах (до NFKC): длиннее — ErrTooLong без
	// хэширования. Check режет раньше, по символам (MaxLength); этот потолок — для Verify при входе.
	MaxInputBytes = 1024

	saltLen    = 16
	minSaltLen = 16
	keyLen     = 32
	minKeyLen  = 16
	maxKeyLen  = 64
)

// DefaultParams — минимум OWASP. Функция, а не переменная: глобальную переменную мог бы
// изменить любой пакет.
func DefaultParams() Params {
	return Params{MemoryKiB: MinMemoryKiB, Iterations: MinIterations, Parallelism: MinParallelism}
}

var ErrMalformed = errors.New("password: хэш не в формате argon2id PHC или параметры вне потолков")

type Hasher struct {
	params Params
	sem    chan struct{}
	dummy  string // хэш случайного пароля для VerifyDummy
}

func NewHasher(p Params, concurrency int) (*Hasher, error) {
	if p.MemoryKiB < MinMemoryKiB || p.Iterations < MinIterations || p.Parallelism < MinParallelism {
		return nil, fmt.Errorf("password: параметры %+v ниже минимума OWASP %+v", p, DefaultParams())
	}
	if !withinCeilings(p) {
		return nil, fmt.Errorf("password: параметры %+v выше потолков (m ≤ %d, t ≤ %d, p ≤ %d)", p,
			MaxMemoryKiB, MaxIterations, MaxParallelism)
	}
	if concurrency < 1 {
		return nil, fmt.Errorf("password: одновременных хэширований %d — нужно ≥ 1", concurrency)
	}
	h := &Hasher{params: p, sem: make(chan struct{}, concurrency)}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	dummy, err := h.Hash(context.Background(), base64.RawStdEncoding.EncodeToString(random))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) (func(), error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	pw, err := normalize(pw)
	if err != nil {
		return "", err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := h.params
	key := argon2.IDKey([]byte(pw), salt, p.Iterations, p.MemoryKiB, p.Parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify — совпадает ли пароль; needsRehash — хэш сделан с параметрами слабее текущих. Пароль
// длиннее MaxInputBytes — ErrTooLong сразу, без хэширования и очереди семафора.
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (bool, bool, error) {
	pw, err := normalize(pw)
	if err != nil {
		return false, false, err
	}
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	defer release()
	got := argon2.IDKey([]byte(pw), salt, p.Iterations, p.MemoryKiB, p.Parallelism, uint32(len(key))) //nolint:gosec // длина ключа из своего хэша
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	weaker := p.MemoryKiB < h.params.MemoryKiB || p.Iterations < h.params.Iterations ||
		p.Parallelism < h.params.Parallelism || len(key) != keyLen
	return true, weaker, nil
}

// VerifyDummy — та же работа на фиктивном хэше: ответ «нет такой почты» по времени не
// отличается от «неверный пароль» (§7.1). Ошибка — отмена ctx или ErrTooLong (как у Verify).
func (h *Hasher) VerifyDummy(ctx context.Context, pw string) error {
	_, _, err := h.Verify(ctx, pw, h.dummy)
	return err
}

func decode(s string) (Params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrMalformed
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return Params{}, nil, nil, ErrMalformed
	}
	var p Params
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Iterations, &p.Parallelism); err != nil || n != 3 ||
		p.MemoryKiB == 0 || p.Iterations == 0 || p.Parallelism == 0 || !withinCeilings(p) {
		return Params{}, nil, nil, ErrMalformed
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	key, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) < minSaltLen || len(key) < minKeyLen || len(key) > maxKeyLen {
		return Params{}, nil, nil, ErrMalformed
	}
	return p, salt, key, nil
}

func withinCeilings(p Params) bool {
	return p.MemoryKiB <= MaxMemoryKiB && p.Iterations <= MaxIterations && p.Parallelism <= MaxParallelism
}

// normalize — потолок длины на входе (до нормализации: NFKC огромной строки — тоже работа) и NFKC.
func normalize(pw string) (string, error) {
	if len(pw) > MaxInputBytes {
		return "", ErrTooLong
	}
	return norm.NFKC.String(pw), nil
}
