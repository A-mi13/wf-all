package i18n

import "strings"

// maxAcceptLanguage — заголовок длиннее не разбирается: настоящие браузеры шлют десятки байт,
// килобайт мусора — не повод тратить на него разбор.
const maxAcceptLanguage = 1024

type langRange struct {
	primary string
	q       int // вес в тысячных: 1..1000
}

// Negotiate — первая поддерживаемая локаль из Accept-Language (RFC 9110: q-веса, '*' игнорируется,
// регион отбрасывается: en-US → en; мусор — пропуск записи; заголовок длиннее 1 КБ — ok=false).
// При равных весах побеждает запись, стоящая раньше. Возвращает написание из supported.
// supported — первичные подтеги (`ru`, `en`): элемент вида `pt-BR` не совпадёт никогда.
// Несколько строк заголовка вызывающий склеивает заранее (httpx.CombineHeaders, через ", ").
func Negotiate(acceptLanguage string, supported []string) (locale string, ok bool) {
	if len(acceptLanguage) > maxAcceptLanguage {
		return "", false
	}
	// Линейный проход без сортировки: строгое «больше» — при равных весах остаётся ранняя запись.
	best := 0
	for el := range strings.SplitSeq(acceptLanguage, ",") {
		r, valid := parseRange(el)
		if !valid || r.q <= best {
			continue
		}
		for _, s := range supported {
			if strings.EqualFold(r.primary, s) {
				locale, ok, best = s, true, r.q
				break
			}
		}
	}
	return locale, ok
}

// parseRange — запись списка: language-range [ OWS ";" OWS "q=" qvalue ]. Пустая, '*', с q=0
// или с нарушением синтаксиса — ok=false (запись пропускается).
func parseRange(el string) (langRange, bool) {
	tag, weight, hasWeight := strings.Cut(el, ";")
	tag = strings.Trim(tag, " \t")
	if tag == "" || tag == "*" {
		return langRange{}, false
	}
	primary, ok := primarySubtag(tag)
	if !ok {
		return langRange{}, false
	}
	q := 1000
	if hasWeight {
		if q, ok = parseQ(strings.Trim(weight, " \t")); !ok {
			return langRange{}, false
		}
	}
	if q == 0 {
		return langRange{}, false
	}
	return langRange{primary: strings.ToLower(primary), q: q}, true
}

// primarySubtag — первый подтег диапазона (RFC 4647: 1*8ALPHA *("-" 1*8alphanum)).
func primarySubtag(tag string) (string, bool) {
	subtags := strings.Split(tag, "-")
	for i, st := range subtags {
		if len(st) < 1 || len(st) > 8 {
			return "", false
		}
		for j := 0; j < len(st); j++ {
			c := st[j]
			alpha := ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
			digit := '0' <= c && c <= '9'
			if !alpha && (!digit || i == 0) {
				return "", false
			}
		}
	}
	return subtags[0], true
}

// parseQ — "q=" qvalue (RFC 9110 §12.4.2): ( "0" [ "." 0*3DIGIT ] ) / ( "1" [ "." 0*3("0") ] ),
// в тысячных. Имя параметра — без учёта регистра.
func parseQ(w string) (int, bool) {
	if len(w) < 3 || (w[0] != 'q' && w[0] != 'Q') || w[1] != '=' {
		return 0, false
	}
	v := w[2:]
	if v[0] != '0' && v[0] != '1' {
		return 0, false
	}
	whole := int(v[0] - '0')
	frac := v[1:]
	if frac != "" {
		if frac[0] != '.' || len(frac) > 4 {
			return 0, false
		}
		frac = frac[1:]
	}
	n := 0
	for i := range 3 {
		d := 0
		if i < len(frac) {
			c := frac[i]
			if c < '0' || c > '9' {
				return 0, false
			}
			d = int(c - '0')
		}
		n = n*10 + d
	}
	if whole == 1 && n != 0 {
		return 0, false
	}
	return whole*1000 + n, true
}
