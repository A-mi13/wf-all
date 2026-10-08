package domain

import (
	"strings"
	"unicode"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

const (
	// maxEmailLen — предел длины хранимого адреса (RFC 5321: путь 256 октетов минус угловые скобки).
	maxEmailLen = 254
	// maxLocalLen — предел локальной части (RFC 5321 §4.5.3.1.1), в хранимой ASCII-форме.
	maxLocalLen = 64
	// maxEmailInput — предел сырого ввода до нормализации: заведомый мусор не гоняем через NFKC и IDNA.
	maxEmailInput = 4 * maxEmailLen
)

// NormalizeEmail — рабочая почта сотрудника в каноническом ASCII-виде. Один и тот же адрес в любом
// написании даёт одну строку, а разные адреса не сливаются в одну и не притворяются друг другом —
// приглашение, принятие, повторная отправка и вход находят одну учётку (Review Focus 1).
//
// Порядок: NFKC (полноширинные буквы и '＠' становятся ASCII), обрезка пробелов по краям, нижний
// регистр; затем ровно одна '@' и непустые части.
//   - Локальная часть — только печатный ASCII (0x21–0x7E), не длиннее 64 байт после NFKC
//     (RFC 5321 §4.5.3.1.1). Адреса SMTPUTF8 (RFC 6531) не принимаем: не-ASCII в локальной
//     части не нормализуется надёжно и даёт двойников, которых глазом не отличить (кириллическая
//     «а», невидимые заполнители) — OWASP Email Validation and Verification Cheat Sheet.
//   - Домен — IDN разрешён и хранится A-label (punycode) по UTS #46, строгий профиль
//     idna.Registration: он отвергает невидимые и запрещённые символы (селекторы вариантов, CGJ,
//     хангыльские заполнители, пробелы нулевой ширины), пустые метки, точку в конце, '_' и дефис
//     на краю метки, битый punycode. До IDNA в домене допускаются только буквы, метки, цифры, '-' и
//     '.': символы категорий S и P (пустой символ Брайля U+2800, эмодзи) IDNA2008 запрещает, а
//     профиль UTS #46 их пропускает.
//   - Общий предел 254 байта — у хранимой ASCII-формы: A-label длиннее исходного написания.
//
// Политика — решение пользователя 08.10.2026 (спека staff-admin-geo2 §4.1).
//
// Смешение письменностей в домене (кириллическая «а» в латинском имени) UTS #46 не запрещает:
// такой домен принимается, но его A-label отличается от латинского, поэтому двойником
// существующей учётки он не становится. ok=false — не адрес.
func NormalizeEmail(s string) (string, bool) {
	if len(s) > maxEmailInput {
		return "", false
	}
	e := strings.ToLower(strings.TrimSpace(norm.NFKC.String(s)))
	local, host, found := strings.Cut(e, "@")
	if !found || local == "" || host == "" || strings.Contains(host, "@") {
		return "", false
	}
	if len(local) > maxLocalLen || !isPrintableASCII(local) {
		return "", false
	}
	for _, r := range host {
		if !unicode.IsLetter(r) && !unicode.Is(unicode.M, r) && !unicode.Is(unicode.Nd, r) && r != '-' && r != '.' {
			return "", false
		}
	}
	ascii, err := idna.Registration.ToASCII(host)
	if err != nil {
		return "", false
	}
	out := local + "@" + strings.ToLower(ascii)
	if len(out) > maxEmailLen || !isPrintableASCII(out) {
		return "", false
	}
	return out, true
}

// isPrintableASCII — только печатные ASCII-символы 0x21–0x7E: без пробела, управляющих и не-ASCII.
func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return true
}
