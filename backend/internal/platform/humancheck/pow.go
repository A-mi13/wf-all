package humancheck

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/humancheck/humancheckdb"
	"wf/backend/internal/platform/keys"
)

const (
	algorithm  = "SHA-256"
	saltBytes  = 12
	minKeySize = 32
)

type PoWConfig struct {
	Keys      []string      // ключи HMAC (base64, ≥ 32 байт): первый подписывает, все проверяют
	TTL       time.Duration // срок жизни задачи
	MaxNumber int64         // сложность: верхняя граница перебора
}

// PoW — серверная часть ALTCHA на своих часах: только SHA-256, kid ключа в соли, ротация
// списком. Использованное решение лежит в humancheck_spent до срока задачи.
type PoW struct {
	signKID string
	verify  map[string][]byte
	ttl     time.Duration
	max     int64
	clock   clock.Clock
	q       *humancheckdb.Queries
}

func NewPoW(db humancheckdb.DBTX, c clock.Clock, cfg PoWConfig) (*PoW, error) {
	if len(cfg.Keys) == 0 {
		return nil, errors.New("humancheck: нет ключей HMAC")
	}
	if cfg.TTL <= 0 || cfg.MaxNumber < 1 {
		return nil, fmt.Errorf("humancheck: срок %s и сложность %d — нужны больше нуля", cfg.TTL, cfg.MaxNumber)
	}
	p := &PoW{verify: map[string][]byte{}, ttl: cfg.TTL, max: cfg.MaxNumber, clock: c, q: humancheckdb.New(db)}
	for i, s := range cfg.Keys {
		k, err := keys.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("humancheck: ключ №%d: %w", i+1, err)
		}
		if len(k) < minKeySize {
			return nil, fmt.Errorf("humancheck: ключ №%d — %d байт, нужно ≥ %d", i+1, len(k), minKeySize)
		}
		kid := keys.ID(k)
		p.verify[kid] = k
		if i == 0 {
			p.signKID = kid
		}
	}
	return p, nil
}

func (p *PoW) NewChallenge(context.Context) (Challenge, error) {
	raw := make([]byte, saltBytes)
	if _, err := rand.Read(raw); err != nil {
		return Challenge{}, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(p.max+1))
	if err != nil {
		return Challenge{}, err
	}
	// параметры — как у ALTCHA: url.Values.Encode (ключи по алфавиту) и «&» в конце
	params := url.Values{"expires": {strconv.FormatInt(p.clock.Now().Add(p.ttl).Unix(), 10)}, "kid": {p.signKID}}
	salt := hex.EncodeToString(raw) + "?" + params.Encode() + "&"
	ch := hashHex(salt + n.String())
	return Challenge{Algorithm: algorithm, Challenge: ch, MaxNumber: p.max, Salt: salt,
		Signature: hmacHex(p.verify[p.signKID], ch)}, nil
}

type payload struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	Number    int64  `json:"number"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

func (p *PoW) Verify(ctx context.Context, solution string) error {
	raw, err := keys.Decode(solution)
	if err != nil {
		return ErrFailed
	}
	var s payload
	if json.Unmarshal(raw, &s) != nil || s.Algorithm != algorithm || s.Number < 0 {
		return ErrFailed
	}
	_, query, ok := strings.Cut(s.Salt, "?")
	if !ok {
		return ErrFailed
	}
	params, err := url.ParseQuery(strings.TrimSuffix(query, "&"))
	if err != nil {
		return ErrFailed
	}
	expires, err := strconv.ParseInt(params.Get("expires"), 10, 64)
	if err != nil {
		return ErrFailed
	}
	// срок — полуинтервал [выдача, expires): в момент expires решение уже не принимается. Строка
	// humancheck_spent живёт до expires, чистка удаляет её только при now > expires — так
	// повтор не проскочит в ту же секунду, когда чистка уже стёрла отметку.
	expiresAt := time.Unix(expires, 0).UTC()
	if !p.clock.Now().Before(expiresAt) {
		return ErrFailed
	}
	key, ok := p.verify[params.Get("kid")]
	if !ok {
		return ErrFailed
	}
	// соль с параметрами входит в хеш, а хеш — под подписью: подделать срок или kid нельзя
	if !equal(hashHex(s.Salt+strconv.FormatInt(s.Number, 10)), s.Challenge) || !equal(hmacHex(key, s.Challenge), s.Signature) {
		return ErrFailed
	}
	n, err := p.q.Spend(ctx, humancheckdb.SpendParams{Signature: s.Signature, ExpiresAt: expiresAt})
	if err != nil {
		return fmt.Errorf("humancheck: %w", err)
	}
	if n == 0 {
		return ErrFailed // повтор
	}
	return nil
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacHex(key []byte, s string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return hex.EncodeToString(m.Sum(nil))
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
