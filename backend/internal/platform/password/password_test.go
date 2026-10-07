package password_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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
	h := hasher(t, password.DefaultParams())
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
	enc, _ := hasher(t, password.DefaultParams()).Hash(ctx, "correct horse battery staple")
	stronger := hasher(t, password.Params{MemoryKiB: 19456, Iterations: 3, Parallelism: 1})
	ok, rehash, err := stronger.Verify(ctx, "correct horse battery staple", enc)
	if err != nil || !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v err=%v", ok, rehash, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	h := hasher(t, password.DefaultParams())
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
	if _, err := password.NewHasher(password.DefaultParams(), 0); err == nil {
		t.Error("семафор 0 принят")
	}
}

// Все места заняты — запрос ждёт не дольше своего ctx.
func TestSemaphoreRespectsContext(t *testing.T) {
	h := hasher(t, password.DefaultParams())
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
	h := hasher(t, password.DefaultParams())
	enc, _ := h.Hash(ctx, "correct horse battery staple")
	const rounds = 5
	verifyTime, dummy := time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	for range rounds {
		start := time.Now()
		_, _, _ = h.Verify(ctx, "wrong", enc)
		verifyTime = min(verifyTime, time.Since(start))
		start = time.Now()
		if err := h.VerifyDummy(ctx, "wrong"); err != nil {
			t.Fatal(err)
		}
		dummy = min(dummy, time.Since(start))
	}
	if dummy < verifyTime/3 {
		t.Fatalf("фиктивная проверка %v против настоящей %v (минимумы из %d) — argon2 пропущен", dummy, verifyTime, rounds)
	}
}

// DefaultParams — константы OWASP; вызывающий не может испортить их для всех.
func TestDefaultParamsAreConstants(t *testing.T) {
	p := password.DefaultParams()
	if p != (password.Params{MemoryKiB: 19456, Iterations: 2, Parallelism: 1}) {
		t.Fatalf("DefaultParams = %+v", p)
	}
	p.MemoryKiB = 1
	if password.DefaultParams().MemoryKiB != 19456 {
		t.Fatal("изменение копии задело умолчания")
	}
}

func TestNewHasherRejectsHugeParams(t *testing.T) {
	for _, p := range []password.Params{
		{MemoryKiB: 256*1024 + 1, Iterations: 2, Parallelism: 1},
		{MemoryKiB: 19456, Iterations: 11, Parallelism: 1},
		{MemoryKiB: 19456, Iterations: 2, Parallelism: 17},
	} {
		if _, err := password.NewHasher(p, 1); err == nil {
			t.Errorf("параметры выше потолка %+v приняты", p)
		}
	}
}

// phc — строка хэша с заданными параметрами, солью и ключом (байты — нули: проверка отвергает
// строку до argon2).
func phc(m, t, p int, saltLen, keyLen int) string {
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", m, t, p, b64(make([]byte, saltLen)), b64(make([]byte, keyLen)))
}

// Хэш из базы с параметрами выше потолка или с короткой солью не проверяется: подменённая строка
// не заставит процесс выделить гигабайты и не пройдёт со слабой солью. Ответ — сразу, без argon2.
func TestVerifyRejectsHostileHash(t *testing.T) {
	h := hasher(t, password.DefaultParams())
	for name, enc := range map[string]string{
		"m выше 256 МиБ":    phc(256*1024+1, 2, 1, 16, 32),
		"m = 4 ГиБ":         phc(4*1024*1024, 2, 1, 16, 32),
		"t выше 10":         phc(19456, 11, 1, 16, 32),
		"p выше 16":         phc(19456, 2, 17, 16, 32),
		"p не влезает в u8": phc(19456, 2, 300, 16, 32),
		"соль 15 байт":      phc(19456, 2, 1, 15, 32),
		"ключ 15 байт":      phc(19456, 2, 1, 16, 15),
		"ключ 65 байт":      phc(19456, 2, 1, 16, 65),
	} {
		start := time.Now()
		if _, _, err := h.Verify(context.Background(), "correct horse battery staple", enc); !errors.Is(err, password.ErrMalformed) {
			t.Errorf("%s: %v, нужен ErrMalformed", name, err)
		}
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Errorf("%s: отказ занял %v — argon2 всё-таки считался", name, d)
		}
	}
}

// На потолке (t = 10, p = 16) хэш ещё считается и проверяется.
func TestCeilingParamsWork(t *testing.T) {
	ctx := context.Background()
	h := hasher(t, password.Params{MemoryKiB: password.MinMemoryKiB, Iterations: password.MaxIterations, Parallelism: password.MaxParallelism})
	enc, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := h.Verify(ctx, "correct horse battery staple", enc); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

// Потолок на входе — до семафора и argon2: гигантский пароль не ждёт очереди и не считается.
func TestInputCeiling(t *testing.T) {
	ctx := context.Background()
	h := hasher(t, password.DefaultParams())
	enc, err := h.Hash(ctx, strings.Repeat("a", password.MaxInputBytes))
	if err != nil {
		t.Fatalf("ровно на потолке: %v", err)
	}
	if ok, _, err := h.Verify(ctx, strings.Repeat("a", password.MaxInputBytes), enc); err != nil || !ok {
		t.Fatalf("проверка на потолке: ok=%v err=%v", ok, err)
	}
	// очередь занята: дошёл бы до семафора — ждал бы ctx и получил DeadlineExceeded
	release := password.Occupy(h)
	defer release()
	wait, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	tooLong := strings.Repeat("a", password.MaxInputBytes+1)
	if _, err := h.Hash(wait, tooLong); !errors.Is(err, password.ErrTooLong) {
		t.Errorf("Hash: %v", err)
	}
	if _, _, err := h.Verify(wait, tooLong, enc); !errors.Is(err, password.ErrTooLong) {
		t.Errorf("Verify: %v", err)
	}
	if err := h.VerifyDummy(wait, tooLong); !errors.Is(err, password.ErrTooLong) {
		t.Errorf("VerifyDummy: %v", err)
	}
}

// NFKC: пароль, набранный полноширинными или разложенными символами, — тот же пароль.
func TestNFKC(t *testing.T) {
	ctx := context.Background()
	h := hasher(t, password.DefaultParams())
	for name, pair := range map[string][2]string{
		"полноширинные": {"ｃｏｒｒｅｃｔ ｈｏｒｓｅ", "correct horse"},
		"составное é":   {"caf\u0065\u0301 au lait", "caf\u00e9 au lait"},
		"лигатура":      {"\ufb01refly window", "firefly window"},
	} {
		enc, err := h.Hash(ctx, pair[0])
		if err != nil {
			t.Fatal(err)
		}
		if ok, _, err := h.Verify(ctx, pair[1], enc); err != nil || !ok {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
}
