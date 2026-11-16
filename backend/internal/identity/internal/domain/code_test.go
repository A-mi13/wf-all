package domain_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	mrand "math/rand/v2"
	"testing"
	"time"

	"wf/backend/internal/identity/internal/domain"
)

// Код из письма — 6 цифр, равномерно: ведущие нули есть, χ² по первой цифре — в пределах (9 степеней свободы,
// 27.88 — квантиль 0.999). Источник — ChaCha8 с фиксированным зерном: тест детерминирован, не флапает.
func TestEmailCodeUniform(t *testing.T) {
	const n = 100_000
	src := mrand.NewChaCha8([32]byte{'w', 'f'})
	var first [10]int
	for range n {
		c, err := domain.NewEmailCode(src)
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != 6 {
			t.Fatalf("%q", c)
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				t.Fatalf("%q", c)
			}
		}
		first[c[0]-'0']++
	}
	if first[0] == 0 {
		t.Fatal("нет кодов с ведущим нулём")
	}
	chi2 := 0.0
	for _, k := range first {
		d := float64(k) - n/10.0
		chi2 += d * d / (n / 10.0)
	}
	if chi2 > 27.88 {
		t.Fatalf("χ² = %.2f по первой цифре: %v", chi2, first)
	}
}

// Отбраковка: значение из «хвоста» uint32 (≥ 4 294 000 000) не берётся по модулю — иначе младшие коды чаще.
func TestEmailCodeRejectsTail(t *testing.T) {
	// 0xFFFFFFFF — хвост, отбрасывается; затем 0x00000001 → 000001
	src := bytes.NewReader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x01})
	c, err := domain.NewEmailCode(src)
	if err != nil || c != "000001" {
		t.Fatalf("%q %v", c, err)
	}
	if _, err := domain.NewEmailCode(bytes.NewReader([]byte{1, 2})); err == nil {
		t.Fatal("короткий источник — без ошибки")
	}
}

func TestNormalizeEmailCode(t *testing.T) {
	for in, want := range map[string]string{"123456": "123456", " 123 456 ": "123456", "１２３４５６": "123456"} {
		if got, ok := domain.NormalizeEmailCode(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"12345", "1234567", "12345a", "123-456", ""} {
		if _, ok := domain.NormalizeEmailCode(in); ok {
			t.Errorf("%q принят", in)
		}
	}
}

func TestCodeTerms(t *testing.T) {
	if domain.CodeTTL != 15*time.Minute || domain.CodeAttempts != 5 || domain.MaxSends != 4 {
		t.Fatalf("%s %d %d", domain.CodeTTL, domain.CodeAttempts, domain.MaxSends)
	}
}

// Паузы повторной отправки: 60 → 120 → 300 с после 1-й, 2-й, 3-й отправки; после 4-й — больше нельзя.
func TestResendSchedule(t *testing.T) {
	last := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for sends, pause := range map[int]time.Duration{1: 60 * time.Second, 2: 120 * time.Second, 3: 300 * time.Second} {
		at, ok := domain.ResendAvailableAt(last, sends)
		if !ok || !at.Equal(last.Add(pause)) {
			t.Errorf("после %d-й: %s %v", sends, at, ok)
		}
	}
	at, _ := domain.ResendAvailableAt(last, 1)
	if now := last.Add(59 * time.Second); !now.Before(at) {
		t.Error("+59 с — уже можно")
	}
	if now := last.Add(60 * time.Second); now.Before(at) {
		t.Error("+60 с — ещё нельзя")
	}
	for _, sends := range []int{4, 5, 0, -1} {
		if _, ok := domain.ResendAvailableAt(last, sends); ok {
			t.Errorf("%d отправок — можно ещё (0 и меньше — не отправлялось, повтора нет)", sends)
		}
	}
}

// Токен шага: 256 бит base64url, в базе — SHA-256; HashStepToken даёт тот же хэш для поиска.
func TestNewStepToken(t *testing.T) {
	raw, hash, err := domain.NewStepToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != 32 {
		t.Fatalf("%q %v", raw, err)
	}
	if sum := sha256.Sum256([]byte(raw)); !bytes.Equal(hash, sum[:]) || !bytes.Equal(domain.HashStepToken(raw), hash) {
		t.Fatal("хэш не SHA-256 токена")
	}
	raw2, _, _ := domain.NewStepToken()
	if raw2 == raw {
		t.Fatal("повтор токена")
	}
}
