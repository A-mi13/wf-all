package domain_test

import (
	"slices"
	"testing"

	"wf/backend/internal/geo/internal/domain"
)

// Реальные строки alternateNamesV2 (RU.txt, выгрузка 02.10.2026): язык, название и флаги — из
// файла; alternateNameId в выдержке не сохранились, поэтому ID — условные, по порядку строк.
// Шаги 1–5 спеки §3.4 от ID не зависят; шаг 6 проверяется отдельными синтетическими строками.
var (
	stavropol487846 = []domain.AltName{
		{ID: 1, Locale: "", Name: "Voroshilovsk"},
		{ID: 2, Locale: "en", Name: "Stavropol", Preferred: true, Short: true},
		{ID: 3, Locale: "", Name: "Ставрополь", Preferred: true},
		{ID: 4, Locale: "ru", Name: "Stavropol’"},
		{ID: 5, Locale: "ru", Name: "Ставрополь"},
	}
	mikhaylovsk493702 = []domain.AltName{
		{ID: 1, Locale: "", Name: "Shpakovskoye"},
		{ID: 2, Locale: "", Name: "Mikhaylovskoye"},
		{ID: 3, Locale: "", Name: "Mikhaylovska"},
		{ID: 4, Locale: "en", Name: "Mikhaylovsk", Preferred: true},
		{ID: 5, Locale: "ru", Name: "Шпаковское"},
		{ID: 6, Locale: "", Name: "Mikhaylovsk"},
		{ID: 7, Locale: "ru", Name: "Михайловск", Preferred: true},
	}
	// Ставропольский край (регион, admin1 70): предпочтительное ru — краткое.
	stavropolKrai = []domain.AltName{
		{ID: 1, Locale: "ru", Name: "Ставрополье", Preferred: true, Short: true},
		{ID: 2, Locale: "ru", Name: "Ставропольский Край"},
	}
)

// without — строки без языка locale: «у места нет кандидатов этого языка» (шаг 7).
func without(names []domain.AltName, locale string) []domain.AltName {
	return slices.DeleteFunc(slices.Clone(names), func(a domain.AltName) bool { return a.Locale == locale })
}

func TestChooseName(t *testing.T) {
	cases := []struct {
		name          string
		cands         []domain.AltName
		locale        string
		countryLocale bool
		fallback      string
		want          string
	}{
		// реальные данные (спека §3.4, «Проверено на данных 02.10.2026»)
		{"Ставрополь ru: шаг 3 — не краткое кириллицей", stavropol487846, "ru", true, "Stavropol", "Ставрополь"},
		{"Ставрополь en: шаг 4 — предпочтительное краткое", stavropol487846, "en", false, "Stavropol", "Stavropol"},
		{"Михайловск ru: шаг 2 — предпочтительное", mikhaylovsk493702, "ru", true, "Mikhaylovsk", "Михайловск"},
		{"Михайловск en: шаг 2 — предпочтительное", mikhaylovsk493702, "en", false, "Mikhaylovsk", "Mikhaylovsk"},
		{"Ставропольский край ru: шаг 3 вместо краткого предпочтительного", stavropolKrai, "ru", true, "Stavropol Kray", "Ставропольский Край"},
		// шаг 7 на реальных строках
		{"нет ru-строк, язык страны: предпочтительное без языка кириллицей", without(stavropol487846, "ru"), "ru", true, "Stavropol", "Ставрополь"},
		{"нет ru-строк, язык страны, без предпочтительного без языка: name", without(mikhaylovsk493702, "ru"), "ru", true, "Mikhaylovsk", "Mikhaylovsk"},
		{"нет en-строк, не язык страны: ascii_name", without(mikhaylovsk493702, "en"), "en", false, "Mikhaylovsk", "Mikhaylovsk"},
		{"шаг 7 только для языка страны: предпочтительное без языка для en не берётся", []domain.AltName{
			{ID: 1, Locale: "", Name: "Stavropolis", Preferred: true},
		}, "en", false, "Stavropol", "Stavropol"},
		// синтетика: исключения шага 1
		{"историческое предпочтительное не берётся", []domain.AltName{
			{ID: 1, Locale: "ru", Name: "Ворошиловск", Preferred: true, Historic: true},
			{ID: 2, Locale: "ru", Name: "Ставрополь"},
		}, "ru", true, "Stavropol", "Ставрополь"},
		{"разговорное предпочтительное не берётся", []domain.AltName{
			{ID: 1, Locale: "ru", Name: "Питер", Preferred: true, Colloquial: true},
			{ID: 2, Locale: "ru", Name: "Санкт-Петербург"},
		}, "ru", true, "Saint Petersburg", "Санкт-Петербург"},
		{"с истёкшим to не берётся", []domain.AltName{
			{ID: 1, Locale: "ru", Name: "Ленинград", Preferred: true, Ended: true},
			{ID: 2, Locale: "ru", Name: "Санкт-Петербург"},
		}, "ru", true, "Saint Petersburg", "Санкт-Петербург"},
		{"пустое название не кандидат", []domain.AltName{
			{ID: 1, Locale: "ru", Name: "  ", Preferred: true},
		}, "ru", false, "Fallback", "Fallback"},
		{"историческое без языка не годится и для шага 7", []domain.AltName{
			{ID: 1, Locale: "", Name: "Ворошиловск", Preferred: true, Historic: true},
		}, "ru", true, "Stavropol", "Stavropol"},
		// синтетика: шаги 5 и 6, ничьи внутри шага
		{"шаг 5: только краткие — кириллица раньше латиницы", []domain.AltName{
			{ID: 1, Locale: "ru", Name: "Stavropolye", Short: true},
			{ID: 2, Locale: "ru", Name: "Ставрополье", Short: true},
		}, "ru", true, "Stavropol", "Ставрополье"},
		{"шаг 6: ни одно правило не выбрало — минимальный ID", []domain.AltName{
			{ID: 20, Locale: "ru", Name: "Bbb"},
			{ID: 10, Locale: "ru", Name: "Aaa"},
		}, "ru", true, "Fallback", "Aaa"},
		{"ничья в шаге 2 — минимальный ID", []domain.AltName{
			{ID: 9, Locale: "en", Name: "Second", Preferred: true},
			{ID: 3, Locale: "en", Name: "First", Preferred: true},
		}, "en", false, "Fallback", "First"},
		{"шаг 2 раньше шага 3: предпочтительное латиницей для ru", []domain.AltName{
			{ID: 1, Locale: "ru", Name: "Москва"},
			{ID: 2, Locale: "ru", Name: "Moskva", Preferred: true},
		}, "ru", true, "Moscow", "Moskva"},
		{"нет кандидатов вовсе", nil, "en", false, "Stavropol", "Stavropol"},
	}
	for _, c := range cases {
		if got := domain.ChooseName(c.cands, c.locale, c.countryLocale, c.fallback); got != c.want {
			t.Errorf("%s: ChooseName(%s) = %q, ждали %q", c.name, c.locale, got, c.want)
		}
	}
}

func TestScriptOf(t *testing.T) {
	cases := []struct {
		locale string
		r      rune
		want   bool
	}{
		{"ru", 'Ж', true}, {"ru", 'ё', true}, {"ru", 'Z', false},
		{"en", 'Z', true}, {"en", 'é', true}, {"en", 'Ж', false},
		{"de", 'a', false}, {"", 'Ж', false},
	}
	for _, c := range cases {
		if got := domain.ScriptOf(c.locale)(c.r); got != c.want {
			t.Errorf("ScriptOf(%q)(%q) = %v, ждали %v", c.locale, c.r, got, c.want)
		}
	}
}

func TestSupportedLocales(t *testing.T) {
	if !slices.Equal(domain.SupportedLocales, []string{"ru", "en"}) {
		t.Fatalf("SupportedLocales = %q, ждали [ru en] (спека §3.3)", domain.SupportedLocales)
	}
}
