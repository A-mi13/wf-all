package i18n_test

import (
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/platform/i18n"
)

var langs = []string{"ru", "en"}

func TestNegotiate(t *testing.T) {
	cases := []struct {
		header string
		want   string // "" — ok=false: поддерживаемого языка нет
	}{
		{"", ""},
		{"ru", "ru"},
		{"en", "en"},
		{"en-US", "en"},
		{"EN-us", "en"},
		{"zh-Hant-TW, en;q=0.1", "en"},
		{"de", ""},
		{"de, en;q=0.5", "en"},
		{"en;q=0.5, ru;q=0.8", "ru"},
		{"en, ru", "en"},
		{"ru;q=0.5, en;q=0.5", "ru"}, // равные веса — порядок заголовка
		// 14 записей: нестабильная сортировка (slices.SortFunc, pdqsort на длинных срезах) отдала бы ru
		{"en,de,de,ru,de,de,de,de,ru;q=0.5,de,de,de,de,de", "en"},
		{"ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7", "ru"},          // браузер
		{"fr-CH, fr;q=0.9, en;q=0.8, de;q=0.7, *;q=0.5", "en"}, // пример MDN
		{" , ,ru", "ru"},     // пустые элементы списка
		{"en ; q=0.5", "en"}, // OWS вокруг ';'
		{"en;Q=0.5", "en"},   // имя q без учёта регистра
		{"en;q=1.000", "en"},
		{"en;q=1.", "en"},
		{"en;q=0.001", "en"},
		// '*' — «любой»: не выбор языка, решает вызывающий (язык страны)
		{"*", ""},
		{"*;q=0.9", ""},
		{"*, en;q=0.1", "en"},
		// мусор — пропуск записи (Review Focus 5)
		{"en-US;q=abc", ""},
		{"en-US;q=abc, ru;q=0.1", "ru"},
		{"en;q=", ""},
		{"en;q=.5", ""},
		{"en;q=1.5", ""},
		{"en;q=1.001", ""},
		{"en;q=0.1234", ""},
		{"en;q=-0.5", ""},
		{"en;q = 0.5", ""},
		{"en;level=1", ""},
		{"en;q=0.5;q=0.6", ""},
		{"en;q=0.5;", ""},
		{"en_US", ""},
		{"en-", ""},
		{"en--US", ""},
		{"-en", ""},
		{"e1", ""},
		{"toolongtag-US", ""},
		{"en-US-abcdefghi", ""},
		{"ру", ""},
		{"\xff\xfe", ""},
		// q=0 — «неприемлемо»
		{"en;q=0", ""},
		{"en;q=0.000", ""},
		{"en;q=0, ru;q=0.1", "ru"},
	}
	for _, c := range cases {
		got, ok := i18n.Negotiate(c.header, langs)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("Negotiate(%q) = %q, %v; нужно %q", c.header, got, ok, c.want)
		}
	}
}

func TestNegotiateSupported(t *testing.T) {
	if got, ok := i18n.Negotiate("ru", nil); ok || got != "" {
		t.Errorf("без поддерживаемых: %q %v", got, ok)
	}
	// возвращается написание из supported, а не из заголовка
	if got, ok := i18n.Negotiate("EN-gb", []string{"en"}); !ok || got != "en" {
		t.Errorf("регистр: %q %v", got, ok)
	}
}

// Заголовок длиннее 1 КБ не разбирается вовсе — даже с поддерживаемым языком (Review Focus 5).
func TestNegotiateLengthLimit(t *testing.T) {
	at := strings.Repeat(" ", 1022) + "ru" // ровно 1024 байта
	if got, ok := i18n.Negotiate(at, langs); !ok || got != "ru" {
		t.Errorf("1024 байта: %q %v, нужно ru", got, ok)
	}
	if got, ok := i18n.Negotiate(" "+at, langs); ok || got != "" {
		t.Errorf("1025 байт: %q %v, нужно ok=false", got, ok)
	}
	if got, ok := i18n.Negotiate("ru, en-"+strings.Repeat("a", 1<<20), langs); ok || got != "" {
		t.Errorf("1 МБ: %q %v, нужно ok=false", got, ok)
	}
}

// Ни на каком вводе не паникует; ok — ровно когда язык из supported.
func FuzzNegotiate(f *testing.F) {
	for _, s := range []string{"", "*", "ru-RU,ru;q=0.9,en;q=0.8", "en-US;q=abc", "en;q=1.0000", ";;;,,,", "\xff\xfe", "-;q=1,"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, header string) {
		got, ok := i18n.Negotiate(header, langs)
		if ok != slices.Contains(langs, got) || (!ok && got != "") || (len(header) > 1024 && ok) {
			t.Fatalf("Negotiate(%q) = %q, %v", header, got, ok)
		}
	})
}
