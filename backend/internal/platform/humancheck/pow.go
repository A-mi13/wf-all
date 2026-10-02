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
	// maxSafeInteger — Number.MAX_SAFE_INTEGER: наибольшее целое, точное в JS
	maxSafeInteger = 1<<53 - 1
)

type PoWConfig struct {
	Keys      []string      // ключи HMAC (base64, ≥ 32 байт): первый подписывает, все проверяют
	TTL       time.Duration // срок жизни задачи, ≥ 1 с
	MaxNumber int64         // сложность: верхняя граница перебора, 1…2^53-1
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

// Keys — разобранные ключи HMAC задач: первый подписывает, все проверяют.
type Keys struct {
	signKID string
	verify  map[string][]byte
}

// ParseKeys — ключи HMAC из окружения (base64, ≥ 32 байт); kid — keys.ID ключа. Без базы:
// cmd/api зовёт до подключения к ней, NewPoW — повторно.
func ParseKeys(list []string) (*Keys, error) {
	if len(list) == 0 {
		return nil, errors.New("humancheck: нет ключей HMAC")
	}
	k := &Keys{verify: make(map[string][]byte, len(list))}
	for i, s := range list {
		b, err := keys.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("humancheck: ключ №%d: %w", i+1, err)
		}
		if len(b) < minKeySize {
			return nil, fmt.Errorf("humancheck: ключ №%d — %d байт, нужно ≥ %d", i+1, len(b), minKeySize)
		}
		kid := keys.ID(b)
		k.verify[kid] = b
		if i == 0 {
			k.signKID = kid
		}
	}
	return k, nil
}

func NewPoW(db humancheckdb.DBTX, c clock.Clock, cfg PoWConfig) (*PoW, error) {
	// expires в соли — Unix-секунды: срок меньше секунды дал бы задачу, истёкшую при выдаче
	if cfg.TTL < time.Second {
		return nil, fmt.Errorf("humancheck: срок задачи %s — нужен ≥ 1s", cfg.TTL)
	}
	// maxNumber уходит в JS-виджет ALTCHA: больше 2^53-1 число там теряет точность; заодно
	// p.max+1 в NewChallenge не переполняется
	if cfg.MaxNumber < 1 || cfg.MaxNumber > maxSafeInteger {
		return nil, fmt.Errorf("humancheck: сложность %d — нужна от 1 до %d", cfg.MaxNumber, int64(maxSafeInteger))
	}
	k, err := ParseKeys(cfg.Keys)
	if err != nil {
		return nil, err
	}
	return &PoW{signKID: k.signKID, verify: k.verify, ttl: cfg.TTL, max: cfg.MaxNumber, clock: c, q: humancheckdb.New(db)}, nil
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
