package logx

import (
	"log/slog"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// local@домен: локальная часть скрывается, домен остаётся для разбора инцидентов.
	// Локальная часть — любые символы, кроме пробелов, кавычек, скобок, разделителей
	// (`=`, `:`, `,`, `;`, `/`, `?`, `&`, `|`), типографскими кавычками и тире и «@»; апостроф
	// допустим, но не первым (`'a@b.co'`).
	emailRe = regexp.MustCompile("[^\\s\"'<>(),;:=@/\\\\\\[\\]{}?&|`\\p{Pi}\\p{Pf}—–][^\\s\"<>(),;:=@/\\\\\\[\\]{}?&|`\\p{Pi}\\p{Pf}—–]*@(?:[\\p{L}\\p{N}\\-]+\\.)+\\p{L}{2,}")
	// хвост UUID перед совпадением: «-» после него склеивает номер с UUID
	uuidTailRe = regexp.MustCompile(`(?i)[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}$`)
	// кандидат в телефон: 10–15 цифр, между ними — пробелы, скобки, дефисы
	phoneRe = regexp.MustCompile(`\+?\d(?:[\s()\-]{0,2}\d){9,14}`)
)

// Mask скрывает персональные данные в строке лога (спека §6.9): почту — до «***@домен»,
// номер телефона — до «***» и двух последних цифр. Цифры, приклеенные к букве или цифре
// (UUID, хеш, адрес, дата), — не телефон и не трогаются; знак препинания рядом с номером
// (`79161234567:`, `79161234567.`, `phone:+7…`) его не защищает — см. gluedBefore/gluedAfter.
func Mask(s string) string {
	if s == "" {
		return s
	}
	s = emailRe.ReplaceAllStringFunc(s, func(m string) string {
		return "***" + m[strings.LastIndex(m, "@"):]
	})
	return maskPhones(s)
}

func maskPhones(s string) string {
	locs := phoneRe.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, l := range locs {
		start, end := l[0], l[1]
		if gluedBefore(s[:start]) || gluedAfter(s[end:]) {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString("***")
		b.WriteString(lastDigits(s[start:end], 2))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// gluedBefore — совпадение продолжает «слово» слева: оно часть UUID, хеша, десятичной дроби
// или адреса. Склеивают буква и цифра; «-» — только после префикса UUID (`xxxxxxxx-xxxx-xxxx-xxxx-`),
// «.» — только после цифры (дробь, IPv4). «:», «_», «=» и прочее — границы: `phone:+7…`,
// `phone_7…`, `phone-7…`.
func gluedBefore(s string) bool {
	r, size := utf8.DecodeLastRuneInString(s)
	if size == 0 {
		return false // начало строки
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '-':
		const uuidPrefixLen = len("xxxxxxxx-xxxx-xxxx-xxxx-")
		return uuidTailRe.MatchString(s[max(0, len(s)-uuidPrefixLen):])
	case '.':
		p, n := utf8.DecodeLastRuneInString(s[:len(s)-size])
		return n > 0 && unicode.IsDigit(p)
	}
	return false
}

// gluedAfter — то же справа. Склеивают буква и цифра; «-», «.», «:» — только если за ними
// снова цифра (дата, адрес с портом, продолжение номера). Иначе это знак препинания:
// `79161234567: blocked`, `звонок на 79161234567.`.
func gluedAfter(s string) bool {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return false // конец строки
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	if strings.ContainsRune("-.:", r) {
		n, nsize := utf8.DecodeRuneInString(s[size:])
		return nsize > 0 && unicode.IsDigit(n)
	}
	return false
}

func lastDigits(s string, n int) string {
	out := make([]byte, 0, n)
	for i := len(s) - 1; i >= 0 && len(out) < n; i-- {
		if s[i] >= '0' && s[i] <= '9' {
			out = append([]byte{s[i]}, out...)
		}
	}
	return string(out)
}

// maskAttr — ReplaceAttr обработчика slog: строки и ошибки проходят через Mask. Сообщение
// (ключ msg) — тоже строковый атрибут.
func maskAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(Mask(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(Mask(err.Error()))
		}
	}
	return a
}
