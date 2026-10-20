package domain

import "unicode"

// zwj — ZERO WIDTH JOINER: склеивает эмодзи-последовательности, единственный допустимый Cf в имени.
const zwj = 0x200D

// DisplayNameOK — отображаемое имя сотрудника без символов, которые подменяют или прячут текст в письмах,
// подписях подтверждений и журнале (M-3 финального ревью): управляющих (Cc — перевод строки, табуляция,
// NUL), форматирующих (Cf — bidi U+202A–202E и U+2066–2069, LRM/RLM, мягкий перенос, BOM, U+200B, теговые
// символы), разделителей строк и абзацев (Zl, Zp) и невидимых «пустых» символов — та же проверка
// isInvisible, что у домена почты (заполнители, пустой шрифт Брайля, селекторы варианта). Исключение —
// ZWJ (эмодзи из нескольких символов). Длину и пустоту проверяет вызывающий.
func DisplayNameOK(name string) bool {
	for _, r := range name {
		if r == zwj {
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || isInvisible(r) {
			return false
		}
	}
	return true
}
