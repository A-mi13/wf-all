package otp

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// CodeLen — символов в резервном коде и коде приглашения (спека сотрудников §4.1): 10 × 5 бит = 50 бит.
	CodeLen = 10
	// RecoveryCount — резервных кодов в поколении (спека сотрудников §4.1, спека identity §3.3).
	RecoveryCount = 10
	// codeAlphabet — base32 RFC 4648 строчными: 32 символа, байт & 31 — равномерно.
	codeAlphabet = "abcdefghijklmnopqrstuvwxyz234567"
)

// errRepeat — источник случайности выдал повтор кода в одном поколении: с crypto/rand это
// невозможно на практике, значит источник сломан.
var errRepeat = errors.New("otp: повтор резервного кода — источник случайности неисправен")

// NewCode — код вида "xxxxx-xxxxx" из CodeLen байт rnd (алфавит RFC 4648 строчными, 50 бит).
// Общий для резервных кодов и кода приглашения: один формат и одна нормализация ввода
// (NormalizeRecoveryCode). Ошибка источника возвращается как есть (io.ErrUnexpectedEOF и т. п.).
func NewCode(rnd io.Reader) (string, error) {
	buf := make([]byte, CodeLen)
	if _, err := io.ReadFull(rnd, buf); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, x := range buf {
		if i == CodeLen/2 {
			b.WriteByte('-')
		}
		b.WriteByte(codeAlphabet[x&31])
	}
	return b.String(), nil
}

// NewRecoveryCodes — RecoveryCount резервных кодов вида "xxxxx-xxxxx" (NewCode), без повторов. В базу
// идёт MAC нормализованного кода (NormalizeRecoveryCode) с меткой модуля, не сам код.
func NewRecoveryCodes(rnd io.Reader) ([]string, error) {
	codes := make([]string, 0, RecoveryCount)
	seen := make(map[string]bool, RecoveryCount)
	for len(codes) < RecoveryCount {
		c, err := NewCode(rnd)
		if err != nil {
			return nil, fmt.Errorf("otp: резервные коды: %w", err)
		}
		if seen[c] {
			return nil, errRepeat
		}
		seen[c] = true
		codes = append(codes, c)
	}
	return codes, nil
}

// NormalizeRecoveryCode — код вида "xxxxx-xxxxx" (резервный или приглашения) в каноническом виде:
// 10 символов алфавита без дефиса. Ввод «как попало»: регистр, пробелы, дефисы и тире, переводы
// строк из .txt (CR LF), BOM и невидимые символы при копировании (isFiller), полноширинные символы
// (NFKC). Любой другой символ или не та длина — ok=false.
func NormalizeRecoveryCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(norm.NFKC.String(s)) {
		switch {
		case isFiller(r) || isDash(r):
			continue
		case r >= 'a' && r <= 'z' || r >= '2' && r <= '7':
			b.WriteRune(r)
		default:
			return "", false
		}
	}
	if b.Len() != CodeLen {
		return "", false
	}
	return b.String(), true
}

// NormalizeCode — 6 цифр кода TOTP после NFKC и удаления пробелов и невидимых символов (isFiller);
// иначе ok=false. Дефис — отказ (правила не расширяются: ответ на мусор — часть поведения).
func NormalizeCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range norm.NFKC.String(s) {
		switch {
		case isFiller(r):
			continue
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			return "", false
		}
	}
	if b.Len() != Digits {
		return "", false
	}
	return b.String(), true
}

// isDash — дефис и тире, которые редакторы подставляют вместо '-'.
func isDash(r rune) bool {
	switch r {
	case '-', '‐', '‑', '‒', '–', '—', '―', '−':
		return true
	}
	return false
}

// isFiller — символ, который при вводе кода пропускается: пробельный (включая \r\n и
// неразрывный) или невидимый форматирующий категории Cf — BOM в начале .txt, пробел нулевой
// ширины и word joiner при копировании. Иначе такой ввод тратил бы попытку второго фактора.
func isFiller(r rune) bool { return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) }
