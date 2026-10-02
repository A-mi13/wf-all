package token

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/testkit/clocktest"
)

// Страж WithValidMethods: даже небрежный keyfunc, отдающий открытый ключ байтами (подмена
// алгоритма на HS256), не должен пропустить токен — алгоритм фиксирует парсер, а не keyfunc.
// Без опции такой токен прошёл бы: HMAC на []byte открытого ключа подпись подтверждает.
func TestParserPinsAlgorithm(t *testing.T) {
	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParseKeys([]string{s})
	if err != nil {
		t.Fatal(err)
	}
	iss := NewIssuer(k, clocktest.New(start))
	pub := k.verify[k.signKID]

	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.NewString(), "sid": uuid.NewString(), "aud": audience,
		"iat": start.Unix(), "exp": start.Add(time.Minute).Unix(),
	})
	forged.Header["kid"] = k.signKID
	raw, err := forged.SignedString([]byte(pub))
	if err != nil {
		t.Fatal(err)
	}

	_, err = iss.parser.ParseWithClaims(raw, &accessClaims{}, func(*jwt.Token) (any, error) {
		return []byte(pub), nil
	})
	if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Fatalf("HS256 на открытом ключе принят парсером: %v", err)
	}
}
