package domain_test

import (
	"strings"
	"testing"

	"wf/backend/internal/geo/internal/domain"
)

func TestCleanName(t *testing.T) {
	ok := []struct{ in, want string }{
		{"  Ставрополь  ", "Ставрополь"},
		{"Ростов-на-Дону", "Ростов-на-Дону"},
		{"Нижний   Новгород", "Нижний Новгород"},
		{"Нижний\u00a0Новгород", "Нижний Новгород"}, // NBSP → пробел (NFKC)
		{"Ｓｔａｖｒｏｐｏｌ", "Stavropol"},                  // полноширинные → ASCII (NFKC)
		{"Ust’-Labinsk", "Ust’-Labinsk"},
		{strings.Repeat("я", 100), strings.Repeat("я", 100)},
		{"  " + strings.Repeat("я", 100) + "  ", strings.Repeat("я", 100)},
		{"Cafe\u0301", "Café"}, // NFKC склеивает ударение — 4 символа
	}
	for _, c := range ok {
		got, valid := domain.CleanName(c.in)
		if !valid || got != c.want {
			t.Errorf("CleanName(%+q) = %+q, %v; ждали %+q, true", c.in, got, valid, c.want)
		}
	}
	bad := map[string]string{
		"пусто":             "",
		"одни пробелы":      "   ",
		"101 символ":        strings.Repeat("я", 101),
		"ZWSP внутри":       "Став\u200bрополь",
		"ZWJ":               "Став\u200dрополь",
		"bidi RLO":          "\u202eлопорватС",
		"мягкий перенос":    "Став\u00adрополь",
		"BOM":               "\ufeffСтаврополь",
		"перевод строки":    "Став\nрополь",
		"табуляция":         "Став\tрополь",
		"NUL":               "Став\x00рополь",
		"разделитель строк": "Став\u2028рополь",
		"хангыльский заполнитель": "Став\u3164рополь",
		"пустой Брайль":           "\u2800",
		"селектор варианта":       "Став\ufe0fрополь",
		"CGJ":                     "Став\u034fрополь",
		"одно ударение":           "\u0301",
		"C1 управляющий":          "Став\u0085рополь",
	}
	for name, in := range bad {
		if got, valid := domain.CleanName(in); valid {
			t.Errorf("%s: CleanName(%+q) = %+q, true; ждали отказ", name, in, got)
		}
	}
}
