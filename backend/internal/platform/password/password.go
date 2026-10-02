// Package password — хэширование паролей (спека бэкенда §6.9, §7.1): argon2id в формате PHC,
// параметры не ниже OWASP (m = 19 МиБ, t = 2, p = 1) из конфига, пересчёт хэша при входе,
// если параметры выросли; число одновременных хэширований ограничено семафором.
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
)

type Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
}

// DefaultParams — минимум OWASP для argon2id.
var DefaultParams = Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1}

const (
	saltLen = 16
	keyLen  = 32
)

var ErrMalformed = errors.New("password: хэш не в формате argon2id PHC")

type Hasher struct {
	params Params
	sem    chan struct{}
	dummy  string // хэш случайного пароля для VerifyDummy
}

func NewHasher(p Params, concurrency int) (*Hasher, error) {
	if p.MemoryKiB < DefaultParams.MemoryKiB || p.Iterations < DefaultParams.Iterations || p.Parallelism < 1 {
		return nil, fmt.Errorf("password: параметры %+v ниже минимума OWASP %+v", p, DefaultParams)
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

// Verify — совпадает ли пароль; needsRehash — хэш сделан с параметрами слабее текущих.
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (bool, bool, error) {
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
// отличается от «неверный пароль» (§7.1). Ошибка — только отмена ctx.
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
		p.MemoryKiB == 0 || p.Iterations == 0 || p.Parallelism == 0 {
		return Params{}, nil, nil, ErrMalformed
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	key, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(salt) == 0 || len(key) == 0 {
		return Params{}, nil, nil, ErrMalformed
	}
	return p, salt, key, nil
}
