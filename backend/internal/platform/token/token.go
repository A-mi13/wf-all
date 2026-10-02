// Package token — access-токены публичного API и примитивы refresh (спека бэкенда §6.2).
// Access — JWT EdDSA (Ed25519) на 10 минут: sub — пользователь, sid — сессия, aud=public.
// Алгоритм зафиксирован на сервере, ключ проверки выбирается по kid из своего набора.
// Сессию на каждый запрос проверяет auth — токен сам по себе не доказывает, что она жива.
package token

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/keys"
)

const (
	AccessTTL = 10 * time.Minute
	audience  = "public"
	// leeway — расхождение часов инстансов API: iat чуть в будущем не повод для 401
	leeway = 5 * time.Second
)

// ErrInvalid — токен не принят: подпись, алгоритм, aud, срок, kid, claims.
var ErrInvalid = errors.New("token: недействительный access-токен")

// Keys — ключи подписи: первый подписывает, все проверяют (ротация списком).
type Keys struct {
	signKID string
	sign    ed25519.PrivateKey
	verify  map[string]ed25519.PublicKey
}

// ParseKeys — сиды Ed25519 (32 байта, base64) из окружения; kid — keys.ID открытого ключа.
func ParseKeys(seeds []string) (*Keys, error) {
	if len(seeds) == 0 {
		return nil, errors.New("token: нет ни одного ключа подписи")
	}
	k := &Keys{verify: make(map[string]ed25519.PublicKey, len(seeds))}
	for i, s := range seeds {
		seed, err := keys.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("token: ключ №%d: %w", i+1, err)
		}
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("token: ключ №%d — %d байт, нужен сид Ed25519 из %d", i+1, len(seed), ed25519.SeedSize)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		kid := keys.ID(pub)
		if _, dup := k.verify[kid]; dup {
			return nil, fmt.Errorf("token: ключ №%d повторяется", i+1)
		}
		k.verify[kid] = pub
		if i == 0 {
			k.signKID, k.sign = kid, priv
		}
	}
	return k, nil
}

// Claims — проверенное содержимое access-токена.
type Claims struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type accessClaims struct {
	jwt.RegisteredClaims
	SID string `json:"sid"`
}

// Issuer — выпуск и проверка access-токенов на наборе ключей.
type Issuer struct {
	keys   *Keys
	clock  clock.Clock
	parser *jwt.Parser
}

// NewIssuer — выпускающий: алгоритм только EdDSA, aud=public, exp обязателен, iat не в будущем.
func NewIssuer(k *Keys, c clock.Clock) *Issuer {
	return &Issuer{keys: k, clock: c, parser: jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(leeway),
		jwt.WithTimeFunc(c.Now),
	)}
}

// Access — access-токен сессии и момент его истечения.
func (i *Issuer) Access(userID, sessionID uuid.UUID) (string, time.Time, error) {
	now := i.clock.Now().Truncate(time.Second) // NumericDate — секунды
	exp := now.Add(AccessTTL)
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		SID: sessionID.String(),
	})
	t.Header["kid"] = i.keys.signKID
	raw, err := t.SignedString(i.keys.sign)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token: подпись: %w", err)
	}
	return raw, exp, nil
}

// Verify — проверка подписи, алгоритма, aud, срока и claims. Любой отказ — ErrInvalid.
func (i *Issuer) Verify(raw string) (Claims, error) {
	var c accessClaims
	_, err := i.parser.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if pub, ok := i.keys.verify[kid]; ok {
			return pub, nil
		}
		return nil, errors.New("неизвестный kid")
	})
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	uid, err := uuid.Parse(c.Subject)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: sub", ErrInvalid)
	}
	sid, err := uuid.Parse(c.SID)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: sid", ErrInvalid)
	}
	if c.IssuedAt == nil {
		return Claims{}, fmt.Errorf("%w: iat", ErrInvalid)
	}
	// exp обязателен уже в парсере (WithExpirationRequired); здесь — не держаться на одной опции
	if c.ExpiresAt == nil {
		return Claims{}, fmt.Errorf("%w: exp", ErrInvalid)
	}
	return Claims{UserID: uid, SessionID: sid, IssuedAt: c.IssuedAt.Time.UTC(), ExpiresAt: c.ExpiresAt.Time.UTC()}, nil
}
