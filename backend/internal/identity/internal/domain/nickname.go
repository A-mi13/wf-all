// Package domain — чистые правила identity без БД и HTTP (спека identity §12): ник, возраст, коды из письма, сроки
// сессий, документы, одноразовые домены, устройство, разбор UA, виды журнала безопасности, токены шагов.
// Константы сроков — только здесь, сценарии их не дублируют.
package domain

import (
	"encoding/hex"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"wf/backend/internal/platform/emailaddr"
)

// NicknameProblem — итог проверки формата ника: NicknameOK или NicknameInvalid (identity.nickname_invalid).
type NicknameProblem string

const (
	NicknameOK      NicknameProblem = ""
	NicknameInvalid NicknameProblem = "invalid"

	NicknameMinLen = 3  // символов после NFKC
	NicknameMaxLen = 20 // символов после NFKC

	// NicknameChangeEvery — смена ника не чаще (спека §5.8).
	NicknameChangeEvery = 30 * 24 * time.Hour
	// NicknameQuarantine — старый ник в карантине после смены или удаления аккаунта.
	NicknameQuarantine = 90 * 24 * time.Hour

	// deletedPrefix — форма заглушки ника удалённого аккаунта (§3.11 п. 3); игроку недоступна (looksDeleted).
	deletedPrefix = "deleted-"
)

// CheckNickname — формат ника (спека §5.8): NFKC, пробелы по краям обрезаются; 3–20 символов; буквы, цифры, '_',
// '.', '-'; знак не первым и не последним, двух знаков подряд нет; хотя бы одна буква (не из одних цифр и знаков);
// невидимые, управляющие, эмодзи — отказ; форма заглушки «deleted<знак>…» — отказ и в написании гомоглифами. Возвращает ник после NFKC. Регистр, двойники латиница/кириллица,
// резерв и карантин — забота базы (normalize_text, 0023).
func CheckNickname(s string) (string, NicknameProblem) {
	n := strings.TrimFunc(norm.NFKC.String(s), unicode.IsSpace)
	if c := utf8.RuneCountInString(n); c < NicknameMinLen || c > NicknameMaxLen {
		return "", NicknameInvalid
	}
	if looksDeleted(n) {
		return "", NicknameInvalid
	}
	prevSign, hasLetter := true, false // prevSign=true в начале: знак первым запрещён
	var prev rune
	for _, r := range n {
		switch {
		case emailaddr.IsInvisible(r) || unicode.IsControl(r):
			return "", NicknameInvalid
		case isSign(r):
			if prevSign {
				return "", NicknameInvalid
			}
			prevSign = true
		case unicode.IsDigit(r):
			prevSign = false
		case unicode.IsLetter(r):
			prevSign, hasLetter = false, true
		case unicode.IsMark(r) && (unicode.IsLetter(prev) || unicode.IsMark(prev)):
			// знаки письменностей без готовых составных (деванагари и т. п.) — только после буквы
			prevSign = false
		default:
			return "", NicknameInvalid
		}
		prev = r
	}
	if prevSign || !hasLetter { // без букв (одни цифры или цифры со знаками) — отказ
		return "", NicknameInvalid
	}
	return n, NicknameOK
}

func isSign(r rune) bool { return r == '_' || r == '.' || r == '-' }

// skeleton — латинские двойники букв слова «deleted» и «l»-подобных символов: кириллица и греческий, «1», «I».
var skeleton = map[rune]rune{
	'е': 'e', 'Е': 'e', 'ё': 'e', 'Ё': 'e', 'ε': 'e', 'Ε': 'e', // е Е ё Ё ε Ε
	'ԁ': 'd', 'Ԁ': 'd', // ԁ Ԁ
	'ӏ': 'l', 'Ӏ': 'l', 'і': 'l', 'І': 'l', 'ı': 'l', '1': 'l', 'I': 'l', '|': 'l', // ӏ Ӏ і І ı 1 I
	'т': 't', 'Т': 't', 'τ': 't', 'Τ': 't', // т Т τ Τ
}

// looksDeleted — ник читается как заглушка удалённого: «deleted» и сразу знак (_ . -), с точностью до регистра,
// гомоглифов (skeleton) и диакритики (NFD, комбинирующие знаки Mn пропускаются — как unaccent в normalize_text;
// Task 7 m7: «délèted-…»). «deletedfan» — обычный ник.
func looksDeleted(n string) bool {
	const word = "deleted"
	i := 0
	for _, r := range norm.NFD.String(n) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if i == len(word) {
			return isSign(r)
		}
		if f, ok := skeleton[r]; ok {
			r = f
		}
		if unicode.ToLower(r) != rune(word[i]) {
			return false
		}
		i++
	}
	return false
}

// DeletedNickname — заглушка ника анонимизированного аккаунта: deleted-<12 hex> из последних 6 байт id — случайной
// части UUIDv7 (первые 6 байт — метка времени: удалённые в одну минуту совпали бы), 20 символов — предел ника.
// Уникальный индекс ника частичный (WHERE deleted_at IS NULL) — заглушки удалённых в нём не участвуют, повтора при
// 23505 нет; 48 бит случайной части — чтобы заглушки различались при чтении.
func DeletedNickname(id uuid.UUID) string {
	return deletedPrefix + hex.EncodeToString(id[10:])
}
