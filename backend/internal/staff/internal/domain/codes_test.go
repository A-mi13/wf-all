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

func TestNewInviteCode(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		want string
	}{
		{"ноль", []byte{0, 0, 0, 0}, "000000"},
		{"ведущие нули", []byte{0, 0, 0, 42}, "000042"},
		{"10^6 → остаток 0", []byte{0x00, 0x0f, 0x42, 0x40}, "000000"},
		{"последнее допустимое 4293999999", []byte{0xff, 0xf1, 0x3d, 0x7f}, "999999"},
		// 4294000000 и выше отбрасываются (смещение остатка), берутся следующие 4 байта
		{"отбор с отказом", []byte{0xff, 0xf1, 0x3d, 0x80, 0, 0, 0, 7}, "000007"},
		{"максимум uint32 отброшен", []byte{0xff, 0xff, 0xff, 0xff, 0, 0, 0x30, 0x39}, "012345"},
	}
	for _, c := range cases {
		got, err := domain.NewInviteCode(bytes.NewReader(c.src))
		if err != nil || got != c.want {
			t.Errorf("%s: NewInviteCode = %q %v, ждали %q", c.name, got, err, c.want)
		}
	}
	if _, err := domain.NewInviteCode(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff})); !errors.Is(err, io.EOF) {
		t.Fatalf("источник иссяк на отказе: %v, ждали io.EOF", err)
	}
}

var recoveryFormat = regexp.MustCompile(`^[a-z2-7]{5}-[a-z2-7]{5}$`)

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
