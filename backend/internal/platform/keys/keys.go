// Package keys — секреты из окружения (спека §6.9: ключи списком для ротации): декодирование
// base64 и имя ключа (kid). Имя выводится из материала — в окружении только сами ключи.
package keys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

var ErrBadKey = errors.New("keys: ключ не в base64")

// Decode — base64 со стандартным или URL-алфавитом, с паддингом или без: так ключи выдают
// и `openssl rand -base64 32`, и generateValue Render.
func Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrBadKey
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, ErrBadKey
}

// ID — имя ключа: первые 8 байт SHA-256 материала в base64url (11 символов). По имени
// находится ключ проверки при ротации; сам ключ из имени не восстановить.
func ID(material []byte) string {
	sum := sha256.Sum256(material)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// Generate — новый ключ: 32 байта из CSPRNG в base64.
func Generate() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
