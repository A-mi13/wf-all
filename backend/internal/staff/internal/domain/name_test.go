package domain_test

import (
	"testing"

	"wf/backend/internal/staff/internal/domain"
)

// M-3 финального ревью: отображаемое имя попадает в письма, подписи подтверждений и журнал — управляющие (Cc),
// форматирующие (Cf, в том числе bidi U+202E и U+2066–2069), разделители строк и невидимые символы отвергаются;
// ZWJ (эмодзи-последовательности) — допустим. Невидимые символы — только escape-последовательностями: в
// исходнике их не видно глазами.
func TestDisplayNameOK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"кириллица", "Ольга В.", true},
		{"латиница и цифры", "Ivan P. 2", true},
		{"дефис и апостроф", "Анна-Мария О'Нил", true},
		{"ZWJ в эмодзи", "Иван \U0001F468\u200D\U0001F4BB", true},
		{"неразрывный пробел внутри", "Иван\u00A0П.", true},
		{"табуляция (Cc)", "Иван\tП.", false},
		{"перевод строки (Cc)", "Иван\nП.", false},
		{"NUL (Cc)", "Иван\x00", false},
		{"DEL (Cc)", "Иван\x7F", false},
		{"NEL (Cc)", "Иван\u0085П.", false},
		{"RLO (bidi)", "Ivan\u202Etxt.exe", false},
		{"LRI…PDI (bidi)", "Иван \u2066x\u2069", false},
		{"LRM (Cf)", "Иван\u200E", false},
		{"мягкий перенос (Cf)", "Ива\u00ADн", false},
		{"BOM (Cf)", "\uFEFFИван", false},
		{"ZWSP (Cf)", "Иван\u200BП.", false},
		{"ZWNJ (Cf)", "Иван\u200C", false},
		{"разделитель строк (Zl)", "Иван\u2028П.", false},
		{"разделитель абзацев (Zp)", "Иван\u2029П.", false},
		{"хангыльский заполнитель", "\u3164", false},
		{"пустой шрифт Брайля", "Иван \u2800", false},
		{"VS16 эмодзи-представление", "Иван \u26BD\uFE0F", true},
		{"VS15 текстовое представление", "Иван \u2764\uFE0E", true},
		{"флаг: VS16 и ZWJ", "\U0001F3F3\uFE0F\u200D\U0001F308", true},
		{"селектор варианта VS1", "Иван\uFE00", false},
		{"селектор варианта VS14", "Иван\uFE0D", false},
		{"дополнительный селектор варианта", "Иван\U000E0100", false},
		{"теговый символ (Cf)", "Иван\U000E0041", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := domain.DisplayNameOK(c.in); got != c.ok {
				t.Fatalf("DisplayNameOK(%+q) = %v, ждали %v", c.in, got, c.ok)
			}
		})
	}
}
