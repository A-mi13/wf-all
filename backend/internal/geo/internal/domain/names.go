package domain

import (
	"strings"
	"unicode"
)

// SupportedLocales — языки названий справочника (спека §3.3); совпадает с языками
// backend/locales (i18n.Supported) — страж в тестах модуля geo. Свой список, а не i18n.Supported:
// domain — только stdlib (чистые правила), а source/app берут языки отсюда.
var SupportedLocales = []string{"ru", "en"}

// AltName — альтернативное название места GeoNames (alternateNamesV2), уже в виде правил:
// Ended — поле to заполнено и в прошлом; считает вызывающий по своим часам (domain без часов).
type AltName struct {
	ID                                     int64
	Locale, Name                           string
	Preferred, Short, Colloquial, Historic bool
	Ended                                  bool
}

// ScriptOf — письменность языка (спека §3.4 шаги 3, 5, 7): ru — кириллица, en — латиница;
// у неизвестного языка письменности нет — ни одна буква ей не принадлежит.
func ScriptOf(locale string) func(rune) bool {
	switch locale {
	case "ru":
		return func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }
	case "en":
		return func(r rune) bool { return unicode.Is(unicode.Latin, r) }
	}
	return func(rune) bool { return false }
}

// ChooseName — название места на языке locale по спеке §3.4 (детерминированно):
//  1. кандидаты — названия с языком locale без isHistoric, isColloquial, с пустым или будущим to;
//  2. предпочтительное и не краткое;
//  3. не краткое и в письменности языка;
//  4. предпочтительное (в том числе краткое);
//  5. в письменности языка;
//  6. минимальный alternateNameId среди кандидатов;
//  7. кандидатов нет: для языка страны (countryLocale) — предпочтительное название без языка
//     в письменности языка; иначе fallback (name GeoNames для языка страны, ascii_name для en).
//
// Внутри шага при нескольких подходящих выигрывает минимальный ID. Пустые названия не кандидаты.
func ChooseName(cands []AltName, locale string, countryLocale bool, fallback string) string {
	script := ScriptOf(locale)
	var pool []AltName
	for _, a := range cands {
		if a.Locale == locale && usable(a) {
			pool = append(pool, a)
		}
	}
	steps := []func(AltName) bool{
		func(a AltName) bool { return a.Preferred && !a.Short },
		func(a AltName) bool { return !a.Short && inScript(a.Name, script) },
		func(a AltName) bool { return a.Preferred },
		func(a AltName) bool { return inScript(a.Name, script) },
		func(AltName) bool { return true },
	}
	for _, ok := range steps {
		if a, found := minID(pool, ok); found {
			return a.Name
		}
	}
	if countryLocale {
		bare := func(a AltName) bool {
			return a.Locale == "" && a.Preferred && usable(a) && inScript(a.Name, script)
		}
		if a, found := minID(cands, bare); found {
			return a.Name
		}
	}
	return fallback
}

// usable — название не историческое, не разговорное, действует и не пустое.
func usable(a AltName) bool {
	return !a.Historic && !a.Colloquial && !a.Ended && strings.TrimSpace(a.Name) != ""
}

// inScript — все буквы названия в письменности is и есть хотя бы одна буква; пробелы, дефисы,
// апострофы и цифры не мешают ("Stavropol’" — латиница).
func inScript(name string, is func(rune) bool) bool {
	letters := 0
	for _, r := range name {
		if !unicode.IsLetter(r) {
			continue
		}
		if !is(r) {
			return false
		}
		letters++
	}
	return letters > 0
}

// minID — подходящее под ok название с минимальным ID.
func minID(pool []AltName, ok func(AltName) bool) (AltName, bool) {
	var best AltName
	found := false
	for _, a := range pool {
		if ok(a) && (!found || a.ID < best.ID) {
			best, found = a, true
		}
	}
	return best, found
}
