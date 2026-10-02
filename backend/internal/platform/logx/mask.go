package logx

import (
	"log/slog"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// local@домен: локальная часть скрывается, домен остаётся для разбора инцидентов
	emailRe = regexp.MustCompile(`[\p{L}\p{N}._%+\-]+@(?:[\p{L}\p{N}\-]+\.)+\p{L}{2,}`)
	// кандидат в телефон: 10–15 цифр, между ними — пробелы, скобки, дефисы
	phoneRe = regexp.MustCompile(`\+?\d(?:[\s()\-]{0,2}\d){9,14}`)
)

// Mask скрывает персональные данные в строке лога (спека §6.9): почту — до «***@домен»,
// номер телефона — до «***» и двух последних цифр. Цифры, приклеенные к букве, точке,
// двоеточию или дефису (UUID, хеш, адрес, дата), — не телефон и не трогаются.
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
		before, _ := utf8.DecodeLastRuneInString(s[:start])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if glued(before) || glued(after) {
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

// glued — символ продолжает «слово»: совпадение — часть UUID, хеша, даты или адреса.
func glued(r rune) bool {
	if r == utf8.RuneError {
		return false // начало или конец строки
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-.:_", r)
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
