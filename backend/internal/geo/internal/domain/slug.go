// Package domain — чистые правила модуля geo (спека geo §3.4, §4.3, §5.3–5.4): без БД, HTTP и часов
// (спека бэкенда §3). Зависимости — стандартная библиотека и golang.org/x/text (NFKC названий).
package domain

import (
	"iter"
	"strconv"
	"strings"
)

// SlugMax — предел длины slug: CHECK cities_slug_format и city_slug_history_slug_format, maxLength
// контракта.
const SlugMax = 100

// Slugify — slug из ascii_name GeoNames (спека §5.3 шаг 4): нижний регистр, всё, что не
// [a-z0-9], — дефис, дефисы схлопнуты и обрезаны по краям. "Stavropol'" → "stavropol".
func Slugify(asciiName string) string {
	var b strings.Builder
	pending := false // перед следующим символом нужен дефис
	for _, r := range strings.ToLower(asciiName) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if pending && b.Len() > 0 {
				b.WriteByte('-')
			}
			pending = false
			b.WriteRune(r)
			continue
		}
		pending = true
	}
	return b.String()
}

// ActivationSlug — базовый slug активируемого города (спека §5.3 шаг 4, R13): Slugify(ascii_name);
// пусто (ascii_name из одних выброшенных символов) — city-<geoname_id>; длиннее SlugMax — обрезка по
// границе части (cutSlug). Результат всегда проходит ValidSlug.
func ActivationSlug(asciiName string, geonameID int64) string {
	s := Slugify(asciiName)
	if s == "" {
		return "city-" + strconv.FormatInt(geonameID, 10)
	}
	return cutSlug(s, SlugMax)
}

// SlugCandidates — бесконечная последовательность кандидатов slug по порядку попыток (спека §5.3
// шаг 4, R13): base, base-<slug региона>, base-2, base-3 … Под каждый суффикс обрезается база
// (cutSlug), чтобы кандидат влез в SlugMax без «--» и хвостового «-». Регион без латинских букв и
// цифр пропускается, как и кандидат, где после обрезки от базы ничего не осталось (суффикс региона
// длиннее предела). Пустая base — пустая последовательность. Сколько попыток — решает вызывающий.
func SlugCandidates(base, regionASCII string) iter.Seq[string] {
	return func(yield func(string) bool) {
		if base == "" {
			return
		}
		if !yield(cutSlug(base, SlugMax)) {
			return
		}
		if region := Slugify(regionASCII); region != "" {
			if s, ok := withSuffix(base, "-"+region); ok && !yield(s) {
				return
			}
		}
		for i := 2; ; i++ {
			s, ok := withSuffix(base, "-"+strconv.Itoa(i))
			if !ok {
				return // суффикс номера длиннее предела — недостижимо на практике
			}
			if !yield(s) {
				return
			}
		}
	}
}

// withSuffix — база, обрезанная под суффикс, плюс суффикс; false — от базы ничего не осталось.
func withSuffix(base, suffix string) (string, bool) {
	room := SlugMax - len(suffix)
	if room <= 0 {
		return "", false
	}
	b := cutSlug(base, room)
	if b == "" {
		return "", false
	}
	return b + suffix, true
}

// cutSlug — slug s (уже в форме Slugify, только ASCII), обрезанный до limit байт: по границе части
// (последний дефис до предела), если разрез пришёлся внутрь части и граница есть; иначе — жёстко.
// Хвостовые дефисы снимаются.
func cutSlug(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if s[limit] != '-' {
		if i := strings.LastIndexByte(cut, '-'); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimRight(cut, "-")
}

// ValidSlug — формат CHECK cities_slug_format: [a-z0-9] частями через одиночный дефис, без дефиса
// по краям, 1..SlugMax символов.
func ValidSlug(s string) bool {
	if s == "" || len(s) > SlugMax || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && s[i-1] != '-':
		default:
			return false
		}
	}
	return true
}
