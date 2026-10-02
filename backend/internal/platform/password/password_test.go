package password_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/password"
)

func hasher(t *testing.T, p password.Params) *password.Hasher {
	t.Helper()
	h, err := password.NewHasher(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashAndVerify(t *testing.T) {
	ctx := context.Background()
	h := hasher(t, password.DefaultParams)
	enc, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("формат: %s", enc)
	}
	if ok, rehash, err := h.Verify(ctx, "correct horse battery staple", enc); err != nil || !ok || rehash {
		t.Fatalf("верный пароль: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, err := h.Verify(ctx, "wrong horse battery staple", enc); err != nil || ok {
		t.Fatalf("неверный пароль принят: %v", err)
	}
	other, _ := h.Hash(ctx, "correct horse battery staple")
	if other == enc {
		t.Fatal("одинаковая соль у двух хэшей")
	}
}

// Параметры выросли — при входе хэш пересчитывается (§7.1).
func TestNeedsRehash(t *testing.T) {
	ctx := context.Background()
	enc, _ := hasher(t, password.DefaultParams).Hash(ctx, "correct horse battery staple")
	stronger := hasher(t, password.Params{MemoryKiB: 19456, Iterations: 3, Parallelism: 1})
	ok, rehash, err := stronger.Verify(ctx, "correct horse battery staple", enc)
	if err != nil || !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v err=%v", ok, rehash, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	h := hasher(t, password.DefaultParams)
	for _, enc := range []string{"", "plain", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x,t=2,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=19456,t=2,p=1$!!$aGFzaA",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdA$aGFzaA"} {
		if _, _, err := h.Verify(context.Background(), "x", enc); !errors.Is(err, password.ErrMalformed) {
			t.Errorf("%q: %v", enc, err)
		}
	}
}

func TestNewHasherRejectsWeakParams(t *testing.T) {
	for _, p := range []password.Params{
		{MemoryKiB: 8192, Iterations: 2, Parallelism: 1},
		{MemoryKiB: 19456, Iterations: 1, Parallelism: 1},
		{MemoryKiB: 19456, Iterations: 2, Parallelism: 0},
	} {
		if _, err := password.NewHasher(p, 1); err == nil {
			t.Errorf("слабые %+v приняты", p)
		}
	}
	if _, err := password.NewHasher(password.DefaultParams, 0); err == nil {
		t.Error("семафор 0 принят")
	}
}

// Все места заняты — запрос ждёт не дольше своего ctx.
func TestSemaphoreRespectsContext(t *testing.T) {
	h := hasher(t, password.DefaultParams)
	release := password.Occupy(h)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "correct horse battery staple"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Hash: %v", err)
	}
	if err := h.VerifyDummy(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("VerifyDummy: %v", err)
	}
}

// Фиктивная проверка стоит столько же, сколько настоящая: «нет почты» и «неверный пароль» по
// времени неразличимы (§7.1). Допуск грубый — тест ловит пропуск argon2, а не микросекунды.
// Сравниваются минимумы из нескольких замеров вперемежку: один замер на нагруженном CI мог
// попасть на паузу планировщика или GC и уронить тест без пропуска argon2.
func TestVerifyDummyCostsLikeVerify(t *testing.T) {
	ctx := context.Background()
	h := hasher(t, password.DefaultParams)
	enc, _ := h.Hash(ctx, "correct horse battery staple")
	const rounds = 5
	real, dummy := time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	for range rounds {
		start := time.Now()
		_, _, _ = h.Verify(ctx, "wrong", enc)
		real = min(real, time.Since(start))
		start = time.Now()
		if err := h.VerifyDummy(ctx, "wrong"); err != nil {
			t.Fatal(err)
		}
		dummy = min(dummy, time.Since(start))
	}
	if dummy < real/3 {
		t.Fatalf("фиктивная проверка %v против настоящей %v (минимумы из %d) — argon2 пропущен", dummy, real, rounds)
	}
}
