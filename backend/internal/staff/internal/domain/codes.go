package domain

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// codeLen — символов в резервном коде и коде приглашения (спека §4.1): 10 × 5 бит = 50 бит.
	codeLen = 10
	// recoveryCount — резервных кодов в поколении (спека §4.1).
	recoveryCount = 10
	// codeAlphabet — base32 RFC 4648 строчными: 32 символа, байт & 31 — равномерно.
	codeAlphabet = "abcdefghijklmnopqrstuvwxyz234567"
)

// errRepeat — источник случайности выдал повтор кода в одном поколении: с crypto/rand это
// невозможно на практике, значит источник сломан.
var errRepeat = errors.New("domain: повтор резервного кода — источник случайности неисправен")

// newCode — код вида "xxxxx-xxxxx" из codeLen байт rnd (алфавит RFC 4648 строчными, 50 бит).
// Общий для резервных кодов и кода приглашения: один формат и одна нормализация ввода (normalizeCode).
func newCode(rnd io.Reader) (string, error) {
	buf := make([]byte, codeLen)
	if _, err := io.ReadFull(rnd, buf); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, x := range buf {
		if i == codeLen/2 {
			b.WriteByte('-')
		}
		b.WriteByte(codeAlphabet[x&31])
	}
	return b.String(), nil
}

// NewInviteCode — код приглашения вида "xxxxx-xxxxx" (решение пользователя 14.10.2026): 50 бит, перебор
// под IP-лимитом недостижим за жизнь приглашения (24 ч), поэтому неверные вводы код не стирают.
// В базе — только шифротекст показанной формы (keys.Ring), сверка — по NormalizeInviteCode.
func NewInviteCode(rnd io.Reader) (string, error) {
	c, err := newCode(rnd)
	if err != nil {
		return "", fmt.Errorf("domain: код приглашения: %w", err)
	}
	return c, nil
}

// NewRecoveryCodes — 10 резервных кодов вида "xxxxx-xxxxx" (алфавит RFC 4648 строчными, 50 бит
// на код). В базу идёт HMAC нормализованного кода (NormalizeRecoveryCode), не сам код.
func NewRecoveryCodes(rnd io.Reader) ([]string, error) {
	codes := make([]string, 0, recoveryCount)
	seen := make(map[string]bool, recoveryCount)
	for len(codes) < recoveryCount {
		c, err := newCode(rnd)
		if err != nil {
			return nil, fmt.Errorf("domain: резервные коды: %w", err)
		}
		if seen[c] {
			return nil, errRepeat
		}
		seen[c] = true
		codes = append(codes, c)
	}
	return codes, nil
}

// NormalizeRecoveryCode — резервный код в каноническом виде (normalizeCode).
func NormalizeRecoveryCode(s string) (string, bool) { return normalizeCode(s) }

// NormalizeInviteCode — код приглашения в каноническом виде (normalizeCode): та же нормализация,
// что у резервных кодов.
func NormalizeInviteCode(s string) (string, bool) { return normalizeCode(s) }

// normalizeCode — код вида "xxxxx-xxxxx" в каноническом виде: 10 символов алфавита без дефиса.
// Ввод «как попало» (Review Focus 5): регистр, пробелы, дефисы и тире, переводы строк из .txt
// (CR LF), BOM и невидимые символы при копировании (isFiller), полноширинные символы (NFKC).
// Любой другой символ или не та длина — ok=false.
func normalizeCode(s string) (string, bool) {
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
	if b.Len() != codeLen {
		return "", false
	}
	return b.String(), true
}

// NormalizeCode — 6 цифр кода TOTP после NFKC и удаления пробелов и невидимых
// символов (isFiller); иначе ok=false.
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
	if b.Len() != TOTPDigits {
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
