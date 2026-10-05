// Package domain — чистые правила модуля geo (спека geo §3.4, §4.3, §5.3–5.4): только
// стандартная библиотека, без БД, HTTP и часов (спека бэкенда §3).
package domain

import (
	"strconv"
	"strings"
)

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

// SlugCandidates — n вариантов slug по порядку попыток (спека §5.3 шаг 4): base,
// base-<slug региона>, base-2, base-3 … Регион без латинских букв и цифр пропускается.
// Пустой base или n ≤ 0 — nil: строить slug не из чего, решает вызывающий.
func SlugCandidates(base, regionASCII string, n int) []string {
	if base == "" || n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	out = append(out, base)
	if region := Slugify(regionASCII); region != "" && len(out) < n {
		out = append(out, base+"-"+region)
	}
	for i := 2; len(out) < n; i++ {
		out = append(out, base+"-"+strconv.Itoa(i))
	}
	return out
}
