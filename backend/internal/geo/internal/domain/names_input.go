package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// NameMax — предел длины названия города, района, региона и страны в символах (maxLength контракта
// GeoNames и GeoDistrictCreate).
const NameMax = 100

// CleanName — название из запроса сотрудника в виде для записи: NFKC (полноширинные и совместимые
// формы → обычные, NBSP → пробел), пробелы по краям обрезаны, внутренние схлопнуты в один. Отказ
// (false): пусто, длиннее NameMax символов, нет ни одного видимого символа (одни метки категории M) или
// есть управляющие (Cc — в том числе перевод строки и табуляция), форматирующие (Cf — bidi, ZWSP, ZWJ,
// мягкий перенос, BOM), разделители строк и абзацев (Zl, Zp) и «пустые» невидимые символы
// (invisibleName). Классы — те же, что у display_name сотрудника (staff/internal/domain.DisplayNameOK),
// но без исключений для эмодзи: в названии места они не нужны, а импортировать чужой internal нельзя.
func CleanName(s string) (string, bool) {
	s = norm.NFKC.String(s)
	for _, r := range s {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || invisibleName(r) {
			return "", false
		}
	}
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	if s == "" || utf8.RuneCountInString(s) > NameMax {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsSpace(r) && !unicode.Is(unicode.M, r) {
			return s, true
		}
	}
	return "", false
}

// invisibleName — символ, который не виден или прячет текст: Cf целиком, Other_Default_Ignorable
// (CGJ, хангыльские заполнители), селекторы варианта и «пустые» символы вне этих свойств — заполнитель
// киданьского письма и пустой шрифт Брайля (тот же список, что у staff/internal/domain.isInvisible).
func invisibleName(r rune) bool {
	if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Variation_Selector, r) {
		return true
	}
	switch r {
	case 0x115F, 0x1160, 0x3164, 0xFFA0, 0x16FE4, 0x2800:
		return true
	}
	return false
}
