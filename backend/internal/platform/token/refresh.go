package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// refreshBytes — 256 бит из CSPRNG (спека §6.2).
const refreshBytes = 32

// successorInfo — контекст HKDF: ключ окна гонки не совпадёт ни с каким другим ключом из
// того же токена.
const successorInfo = "wf refresh successor v1"

var ErrSealed = errors.New("token: преемник не открывается")

// NewRefresh — refresh-токен (base64url без паддинга) и его SHA-256 — в sessions хранится хэш.
func NewRefresh() (string, []byte, error) {
	b := make([]byte, refreshBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, HashRefresh(raw), nil
}

// HashRefresh — SHA-256 refresh-токена: по нему сессия ищется в базе.
func HashRefresh(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// SealSuccessor — преемник для окна гонки 15 с (§6.2), зашифрованный AES-256-GCM ключом из
// HKDF-SHA256 от предъявленного refresh: открыть его может только тот, у кого есть
// предъявленный токен; в базе преемник открытым не лежит. Формат: nonce || шифротекст.
func SealSuccessor(presented, successor string) ([]byte, error) {
	aead, err := successorAEAD(presented)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(successor), nil), nil
}

// OpenSuccessor — преемник, запечатанный SealSuccessor; чужой токен или порча — ErrSealed.
func OpenSuccessor(presented string, sealed []byte) (string, error) {
	aead, err := successorAEAD(presented)
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", ErrSealed
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
	if err != nil {
		return "", ErrSealed
	}
	return string(plain), nil
}

func successorAEAD(presented string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, []byte(presented), nil, successorInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("token: hkdf: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
