package domain

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	// inviteModulo — 10^6: код приглашения — 6 цифр.
	inviteModulo = 1_000_000
	// inviteLimit — наибольшее кратное 10^6, не превышающее 2^32: значения не ниже отбрасываются,
	// иначе младшие коды выпадали бы чаще (смещение остатка).
	inviteLimit = (1 << 32) / inviteModulo * inviteModulo

	// recoveryCount — резервных кодов в поколении, recoveryLen — символов в коде (спека §4.1).
	recoveryCount = 10
	recoveryLen   = 10
	// recoveryAlphabet — base32 RFC 4648 строчными: 32 символа, байт & 31 — равномерно.
	recoveryAlphabet = "abcdefghijklmnopqrstuvwxyz234567"
)

// errRepeat — источник случайности выдал повтор кода в одном поколении: с crypto/rand это
// невозможно на практике, значит источник сломан.
var errRepeat = errors.New("domain: повтор резервного кода — источник случайности неисправен")

// NewInviteCode — код приглашения: 6 цифр, равномерно (отбор с отказом по 4 байтам rnd).
func NewInviteCode(rnd io.Reader) (string, error) {
	var b [4]byte
	for {
		if _, err := io.ReadFull(rnd, b[:]); err != nil {
			return "", fmt.Errorf("domain: код приглашения: %w", err)
		}
		if v := binary.BigEndian.Uint32(b[:]); v < inviteLimit {
			return fmt.Sprintf("%06d", v%inviteModulo), nil
		}
	}
}

// NewRecoveryCodes — 10 резервных кодов вида "xxxxx-xxxxx" (алфавит RFC 4648 строчными, 50 бит
// на код). В базу идёт HMAC нормализованного кода (NormalizeRecoveryCode), не сам код.
func NewRecoveryCodes(rnd io.Reader) ([]string, error) {
	codes := make([]string, 0, recoveryCount)
	seen := make(map[string]bool, recoveryCount)
	buf := make([]byte, recoveryLen)
	for len(codes) < recoveryCount {
		if _, err := io.ReadFull(rnd, buf); err != nil {
			return nil, fmt.Errorf("domain: резервные коды: %w", err)
		}
		var b strings.Builder
		for i, x := range buf {
			if i == recoveryLen/2 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[x&31])
		}
		c := b.String()
		if seen[c] {
			return nil, errRepeat
		}
		seen[c] = true
		codes = append(codes, c)
	}
	return codes, nil
}

// NormalizeRecoveryCode — резервный код в каноническом виде: 10 символов алфавита без дефиса.
// Ввод «как попало» (Review Focus 5): регистр, пробелы, дефисы и тире, переводы строк из .txt
// (\r\n), полноширинные символы (NFKC). Любой другой символ или не та длина — ok=false.
func NormalizeRecoveryCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(norm.NFKC.String(s)) {
		switch {
		case unicode.IsSpace(r) || isDash(r):
			continue
		case r >= 'a' && r <= 'z' || r >= '2' && r <= '7':
			b.WriteRune(r)
		default:
			return "", false
		}
	}
	if b.Len() != recoveryLen {
		return "", false
	}
	return b.String(), true
}

// NormalizeCode — 6 цифр кода (TOTP, приглашение) после NFKC и удаления пробелов;
// иначе ok=false.
func NormalizeCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range norm.NFKC.String(s) {
		switch {
		case unicode.IsSpace(r):
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
	case '-', '‐', '‑', '‒', '–', '—', '−':
		return true
	}
	return false
}
