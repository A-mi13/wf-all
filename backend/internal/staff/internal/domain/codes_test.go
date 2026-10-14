package domain_test

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/staff/internal/domain"
)

var recoveryFormat = regexp.MustCompile(`^[a-z2-7]{5}-[a-z2-7]{5}$`)

// Код приглашения (решение пользователя 14.10.2026): 10 символов base32 RFC 4648 строчными в виде
// xxxxx-xxxxx (50 бит) — перебор под IP-лимитом недостижим, неверные вводы код не стирают.
func TestNewInviteCode(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		want string
	}{
		// байт b → алфавит[b & 31]
		{"подряд", []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, "abcde-fghij"},
		{"старшие биты отброшены", []byte{32, 33, 255, 30, 31, 0, 0, 0, 0, 0}, "ab767-aaaaa"},
	}
	for _, c := range cases {
		got, err := domain.NewInviteCode(bytes.NewReader(c.src))
		if err != nil || got != c.want {
			t.Errorf("%s: NewInviteCode = %q %v, ждали %q", c.name, got, err, c.want)
		}
	}
	src := []byte{0, 37, 74, 111, 148, 185, 222, 3, 40, 77}
	got, err := domain.NewInviteCode(bytes.NewReader(src))
	if err != nil || !recoveryFormat.MatchString(got) {
		t.Fatalf("код %q %v не в формате xxxxx-xxxxx", got, err)
	}
	if norm, ok := domain.NormalizeInviteCode(got); !ok || norm != got[:5]+got[6:] {
		t.Fatalf("сгенерированный %q нормализован в %q %v", got, norm, ok)
	}
	if _, err := domain.NewInviteCode(bytes.NewReader(src[:9])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("короткий источник: %v, ждали io.ErrUnexpectedEOF", err)
	}
}

// Ввод кода приглашения нормализуется так же, как резервный код: регистр, дефис и тире, пробелы,
// \r\n, NFKC; цифры TOTP-формата (6 цифр) и мусор — отказ.
func TestInviteCodeNormalize(t *testing.T) {
	for _, in := range []string{
		"k7m2p-x4q6d", "K7M2P-X4Q6D", "k7m2px4q6d", "  k7m2p x4q6d  ", "k7m2p-x4q6d\r\n",
		"K7M2P – X4Q6D", "Ｋ７Ｍ２Ｐ－Ｘ４Ｑ６Ｄ", "\ufeffk7m2p-x4q6d",
	} {
		if got, ok := domain.NormalizeInviteCode(in); !ok || got != "k7m2px4q6d" {
			t.Errorf("NormalizeInviteCode(%q) = %q %v, ждали k7m2px4q6d true", in, got, ok)
		}
	}
	for _, in := range []string{"", "305718", "k7m2p-x4q9", "k7m2p-x4q6dd", "k7m1p-x4q6d", "k7m2p_x4q6d", "к7м2р-х4q9d"} {
		if got, ok := domain.NormalizeInviteCode(in); ok {
			t.Errorf("NormalizeInviteCode(%q) = %q, ждали отказ", in, got)
		}
	}
}

func TestNewRecoveryCodes(t *testing.T) {
	src := make([]byte, 100)
	for i := range src {
		src[i] = byte(i)
	}
	codes, err := domain.NewRecoveryCodes(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("кодов %d, ждали 10", len(codes))
	}
	for _, c := range codes {
		if !recoveryFormat.MatchString(c) {
			t.Errorf("код %q не в формате xxxxx-xxxxx (RFC 4648 строчными)", c)
		}
	}
	// байт b → алфавит[b & 31]: 0..9 → a..j, 10..19 → k..t, 20..29 → u..z,2..5, 30..39 → 6,7,a..h
	if want := []string{"abcde-fghij", "klmno-pqrst", "uvwxy-z2345", "67abc-defgh"}; !slices.Equal(codes[:4], want) {
		t.Fatalf("первые коды %q, ждали %q", codes[:4], want)
	}
	// источник из нулей: второй код повторяет первый — ошибка именно о повторе, а не любая
	if _, err := domain.NewRecoveryCodes(bytes.NewReader(make([]byte, 100))); err == nil ||
		!strings.Contains(err.Error(), "повтор резервного кода") {
		t.Fatalf("источник из нулей: %v, ждали ошибку повтора резервного кода", err)
	}
	if _, err := domain.NewRecoveryCodes(bytes.NewReader(src[:95])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("короткий источник: %v", err)
	}
}

// Review Focus 5: ввод резервного кода «как попало» нормализуется в одну строку.
func TestRecoveryCodeNormalize(t *testing.T) {
	ok := []string{
		"abcde-fghij",
		"ABCDE-FGHIJ",
		"abcdefghij",
		"  abcde fghij  ",
		"abcde-fghij\r\n",
		"\tAbCdE – FgHiJ\n", // тире вместо дефиса
		"abc de-fg hij",
		"ＡＢＣＤＥ－ＦＧＨＩＪ",       // полноширинные (NFKC)
		"abcde\u00a0fghij",  // неразрывный пробел
		"\ufeffabcde-fghij", // BOM в начале .txt (Cf)
		"abcde\u200b-fghij", // пробел нулевой ширины при копировании (Cf)
		"abcde\u2015fghij",  // горизонтальная черта (NFKC её не меняет)
	}
	for _, in := range ok {
		got, valid := domain.NormalizeRecoveryCode(in)
		if !valid || got != "abcdefghij" {
			t.Errorf("NormalizeRecoveryCode(%q) = %q %v, ждали abcdefghij true", in, got, valid)
		}
	}
	bad := []string{
		"",
		"abcde-fghi",    // 9 символов
		"abcde-fghijk",  // 11 символов
		"abcde-fgh1j",   // 1 — не из алфавита RFC 4648
		"abcde-fgh0j",   // 0
		"abcde-fgh8j",   // 8
		"abcde_fghij",   // подчёркивание — не разделитель
		"абвгд-еёжзи",   // кириллица
		"abcde-fghij;x", // мусор в хвосте
	}
	for _, in := range bad {
		if got, valid := domain.NormalizeRecoveryCode(in); valid {
			t.Errorf("NormalizeRecoveryCode(%q) = %q, ждали отказ", in, got)
		}
	}
	// код из генератора нормализуется в себя без дефиса
	codes, err := domain.NewRecoveryCodes(bytes.NewReader(bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, 10)))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range codes {
		if got, valid := domain.NormalizeRecoveryCode(c); !valid || got != c[:5]+c[6:] {
			t.Errorf("сгенерированный %q нормализован в %q %v", c, got, valid)
		}
	}
}

func TestNormalizeCode(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"123456", "123456", true},
		{" 123 456 ", "123456", true},
		{"123456\r\n", "123456", true},
		{"１２３４５６", "123456", true}, // полноширинные цифры (NFKC)
		{"000000", "000000", true},
		{"\ufeff123456", "123456", true}, // BOM (Cf)
		{"123\u200b456", "123456", true}, // пробел нулевой ширины (Cf)
		{"123456\u2060", "123456", true}, // word joiner (Cf)
		{"12345", "", false},
		{"1234567", "", false},
		{"123\u200b4567", "", false}, // Cf пропускается, но цифр после этого 7, а не 6
		{"123-456", "", false},
		{"12345a", "", false},
		{"", "", false},
		{"١٢٣٤٥٦", "", false}, // арабские цифры — не ASCII после NFKC
	}
	for _, c := range cases {
		got, ok := domain.NormalizeCode(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeCode(%q) = %q %v, ждали %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
