package domain

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// maxEmailLen — предел длины адреса (RFC 5321: путь 256 октетов минус угловые скобки).
const maxEmailLen = 254

// NormalizeEmail — рабочая почта сотрудника в каноническом виде: NFKC (полноширинные буквы и
// '＠' становятся ASCII), обрезка пробелов по краям, нижний регистр. Один и тот же адрес в любом
// написании даёт одну строку — приглашение, принятие, повторная отправка и вход находят одну
// учётку (Review Focus 1). ok=false — не адрес: не ровно одна '@', пустая часть, пробел,
// управляющий (Cc) или невидимый форматирующий (Cf: пробел нулевой ширины, BOM, смена
// направления письма) символ внутри, длиннее 254 байт. Cf NFKC не убирает, а TrimSpace не
// срезает: без отказа "<U+200B>maria@…" стал бы двойником учётки "maria@…".
func NormalizeEmail(s string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(norm.NFKC.String(s)))
	if e == "" || len(e) > maxEmailLen {
		return "", false
	}
	local, host, found := strings.Cut(e, "@")
	if !found || local == "" || host == "" || strings.Contains(host, "@") {
		return "", false
	}
	for _, r := range e {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return e, true
}
