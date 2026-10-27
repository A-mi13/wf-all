package domain

import "unicode"

// Допустимые невидимые символы имени — только внутри эмодзи: ZWJ склеивает эмодзи-последовательности (единственный
// допустимый Cf), VS15/VS16 выбирают текстовое или эмодзи-представление. Прочие селекторы варианта
// (U+FE00–FE0D, U+E0100+) — отказ, как у isInvisible.
const (
	zwj  = 0x200D
	vs15 = 0xFE0E
	vs16 = 0xFE0F
)

// DisplayNameOK — отображаемое имя сотрудника без символов, которые подменяют или прячут текст в письмах,
// подписях подтверждений и журнале (M-3 финального ревью): управляющих (Cc — перевод строки, табуляция,
// NUL), форматирующих (Cf — bidi U+202A–202E и U+2066–2069, LRM/RLM, мягкий перенос, BOM, U+200B, теговые
// символы), разделителей строк и абзацев (Zl, Zp) и невидимых «пустых» символов — та же проверка
// isInvisible, что у домена почты (заполнители, пустой шрифт Брайля, селекторы варианта).
//
// Исключения — только внутри эмодзи (волна 2, m1: иначе невидимая подпись из одних ZWJ или двойник
// «Иван<ZWJ>Петров»):
//   - ZWJ — только между пиктограммами (Extended_Pictographic): слева пиктограмма, возможно с селектором
//     представления и модификатором тона кожи, справа — пиктограмма (правило склейки GB11, UAX #29);
//   - VS15/VS16 — только сразу после пиктограммы или основы кейкапа (0–9, #, *): не первым, не после ZWJ, другого
//     селектора, пробела или буквы (двойник «Иван<VS16>»).
//
// И хотя бы один видимый символ (не пробел, не ZWJ, не селектор и не метка категории M). Длину и крайние пробелы проверяет вызывающий.
func DisplayNameOK(name string) bool {
	rs := []rune(name)
	visible := false
	for i, r := range rs {
		switch {
		case r == zwj:
			if !joinsPictographs(rs, i) {
				return false
			}
		case r == vs15 || r == vs16:
			if i == 0 || !presentationBase(rs[i-1]) {
				return false
			}
		case unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || isInvisible(r):
			return false
		case !unicode.IsSpace(r) && !unicode.Is(unicode.M, r):
			// метка (Mn, Mc, Me) без основы — не видимый символ: имя из одних ударений или U+20DD — пустая подпись
			visible = true
		}
	}
	return visible
}

// joinsPictographs — ZWJ на позиции i стоит между пиктограммами: справа сразу пиктограмма, слева — пиктограмма,
// после которой допустимы только селектор представления и модификатор тона кожи (в этом порядке или по одному).
func joinsPictographs(rs []rune, i int) bool {
	if i+1 >= len(rs) || !unicode.Is(pictographic, rs[i+1]) {
		return false
	}
	j := i - 1
	if j >= 0 && (rs[j] == vs15 || rs[j] == vs16) {
		j--
	}
	if j >= 0 && skinTone(rs[j]) {
		j--
	}
	return j >= 0 && unicode.Is(pictographic, rs[j])
}

// presentationBase — символ, после которого селектор представления (VS15/VS16) осмыслен: пиктограмма или основа
// кейкапа (UTS #51, emoji-variation-sequences: прочие основы — пиктограммы).
func presentationBase(r rune) bool {
	return unicode.Is(pictographic, r) || r >= '0' && r <= '9' || r == '#' || r == '*'
}

// skinTone — модификатор тона кожи (Emoji_Modifier, U+1F3FB–1F3FF).
func skinTone(r rune) bool {
	return r >= 0x1F3FB && r <= 0x1F3FF
}
