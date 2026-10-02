package humancheck_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go"

	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newKey(t *testing.T) string {
	t.Helper()
	k, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newPoW(t *testing.T, pools dbtest.Pools, c *clocktest.Fake, ks ...string) *humancheck.PoW {
	t.Helper()
	p, err := humancheck.NewPoW(pools.As, c, humancheck.PoWConfig{Keys: ks, TTL: 5 * time.Minute, MaxNumber: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// solve — решает задачу официальной библиотекой ALTCHA, как виджет в браузере.
func solve(t *testing.T, ch humancheck.Challenge) string {
	t.Helper()
	sol, err := altcha.SolveChallenge(ch.Challenge, ch.Salt, altcha.SHA256, int(ch.MaxNumber), 0, nil)
	if err != nil || sol == nil {
		t.Fatalf("задача не решается: %v", err)
	}
	b, err := json.Marshal(altcha.Payload{Algorithm: ch.Algorithm, Challenge: ch.Challenge,
		Number: int64(sol.Number), Salt: ch.Salt, Signature: ch.Signature})
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Совместимость с протоколом ALTCHA в обе стороны: задачу решает библиотека, а наше решение
// принимает её проверка (закрывает §14 спеки).
func TestPoWCompatibleWithALTCHA(t *testing.T) {
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	key := newKey(t)
	pow := newPoW(t, pools, clocktest.New(start), key)
	ch, err := pow.NewChallenge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Algorithm != "SHA-256" || ch.MaxNumber != 1000 {
		t.Fatalf("задача: %+v", ch)
	}
	s := solve(t, ch)
	raw, _ := keys.Decode(key)
	if ok, err := altcha.VerifySolution(s, string(raw), false); err != nil || !ok {
		t.Fatalf("библиотека ALTCHA не приняла: %v", err)
	}
	if err := pow.Verify(ctx, s); err != nil {
		t.Fatalf("решение не принято: %v", err)
	}
}

// Review Focus 5.
func TestPoWRejects(t *testing.T) {
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	c := clocktest.New(start)
	oldK, nextK := newKey(t), newKey(t)
	pow := newPoW(t, pools, c, oldK)

	ch, _ := pow.NewChallenge(ctx)
	s := solve(t, ch)
	if err := pow.Verify(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := pow.Verify(ctx, s); !errors.Is(err, humancheck.ErrFailed) {
		t.Fatalf("повтор решения принят: %v", err)
	}

	ch, _ = pow.NewChallenge(ctx)
	late := solve(t, ch)
	c.Advance(5*time.Minute + time.Second)
	if err := pow.Verify(ctx, late); !errors.Is(err, humancheck.ErrFailed) {
		t.Fatalf("просроченное принято: %v", err)
	}
	c.Set(start)

	// ротация: старый ключ ещё в списке — принимается; выведен — нет
	ch, _ = pow.NewChallenge(ctx)
	byOld := solve(t, ch)
	ch2, _ := pow.NewChallenge(ctx)
	byOld2 := solve(t, ch2)
	if err := newPoW(t, pools, c, nextK, oldK).Verify(ctx, byOld); err != nil {
		t.Fatalf("ротация: старый ключ отвергнут: %v", err)
	}
	if err := newPoW(t, pools, c, nextK).Verify(ctx, byOld2); !errors.Is(err, humancheck.ErrFailed) {
		t.Fatalf("ключ выведен, а решение принято: %v", err)
	}

	tamper := func(f func(p *altcha.Payload)) string {
		ch, _ := pow.NewChallenge(ctx)
		raw, _ := base64.StdEncoding.DecodeString(solve(t, ch))
		var p altcha.Payload
		_ = json.Unmarshal(raw, &p)
		f(&p)
		b, _ := json.Marshal(p)
		return base64.StdEncoding.EncodeToString(b)
	}
	cases := map[string]string{
		"чужое число":    tamper(func(p *altcha.Payload) { p.Number++ }),
		"алгоритм SHA-1": tamper(func(p *altcha.Payload) { p.Algorithm = "SHA-1" }),
		"подпись подменена": tamper(func(p *altcha.Payload) {
			flip := byte('0')
			if p.Signature[0] == '0' {
				flip = '1'
			}
			p.Signature = string(flip) + p.Signature[1:]
		}),
		"срок в соли продлён": tamper(func(p *altcha.Payload) {
			p.Salt = p.Salt[:24] + "?expires=9999999999&" + p.Salt[strings.Index(p.Salt, "kid="):]
		}),
		"отрицательное число": tamper(func(p *altcha.Payload) { p.Number = -1 }),
		"не base64":           "%%%",
		"не JSON":             base64.StdEncoding.EncodeToString([]byte("nope")),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if err := pow.Verify(ctx, s); !errors.Is(err, humancheck.ErrFailed) {
				t.Fatalf("принято: %v", err)
			}
		})
	}
}

// Граница срока: в секунду expires решение уже не принимается. Иначе чистка (expires_at < now)
// может стереть отметку о расходе, пока решение ещё живо, — и повтор пройдёт.
func TestPoWExpiresAtBoundary(t *testing.T) {
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	c := clocktest.New(start)
	pow := newPoW(t, pools, c, newKey(t))
	ch, err := pow.NewChallenge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := solve(t, ch)
	c.Advance(5 * time.Minute)
	if err := pow.Verify(ctx, s); !errors.Is(err, humancheck.ErrFailed) {
		t.Fatalf("решение принято в момент expires: %v", err)
	}
	c.Set(start.Add(5*time.Minute - time.Nanosecond))
	if err := pow.Verify(ctx, s); err != nil {
		t.Fatalf("решение отвергнуто до expires: %v", err)
	}
}

func TestNewPoWValidatesConfig(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "api")
	c := clocktest.New(start)
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	for name, cfg := range map[string]humancheck.PoWConfig{
		"нет ключей":        {TTL: time.Minute, MaxNumber: 1000},
		"короткий ключ":     {Keys: []string{short}, TTL: time.Minute, MaxNumber: 1000},
		"нулевой срок":      {Keys: []string{newKey(t)}, MaxNumber: 1000},
		"нулевая сложность": {Keys: []string{newKey(t)}, TTL: time.Minute},
	} {
		if _, err := humancheck.NewPoW(pools.As, c, cfg); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
}
