package domain

import (
	"strings"
	"unicode"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

const (
	// maxEmailLen — предел длины хранимого адреса (RFC 5321: путь 256 октетов минус угловые скобки).
	maxEmailLen = 254
	// maxLocalLen — предел локальной части (RFC 5321 §4.5.3.1.1), в хранимой ASCII-форме.
	maxLocalLen = 64
	// maxEmailInput — предел сырого ввода до нормализации: заведомый мусор не гоняем через NFKC и IDNA.
	maxEmailInput = 4 * maxEmailLen
	// atextSpecials — спецсимволы atext (RFC 5322 §3.2.3) помимо букв и цифр.
	atextSpecials = "!#$%&'*+-/=?^_`{|}~"
)

// NormalizeEmail — рабочая почта сотрудника в каноническом ASCII-виде. Один и тот же адрес в любом
// написании даёт одну строку, а разные адреса не сливаются в одну и не притворяются друг другом —
// приглашение, принятие, повторная отправка и вход находят одну учётку (Review Focus 1).
//
// Порядок: NFKC (полноширинные буквы и '＠' становятся ASCII), обрезка пробелов по краям, нижний
// регистр; затем ровно одна '@' и непустые части.
//   - Локальная часть — dot-atom RFC 5322 (§3.2.3): только atext и одиночные точки не на краю, не
//     длиннее 64 байт после NFKC (RFC 5321 §4.5.3.1.1). Кавычки, скобки, запятые и прочее, что
//     dot-atom не допускает, — отказ: иначе адрес примут, а письмо упадёт в SMTP-клиенте уже после
//     создания приглашения. Адреса SMTPUTF8 (RFC 6531) не принимаем: не-ASCII в локальной части не
//     нормализуется надёжно и даёт двойников, которых глазом не отличить (кириллическая «а»,
//     невидимые заполнители) — OWASP Email Validation and Verification Cheat Sheet.
//   - Домен — IDN разрешён и хранится A-label (punycode) по UTS #46, строгий профиль
//     idna.Registration: он отвергает селекторы вариантов, CGJ, хангыльские заполнители, пробелы
//     нулевой ширины, пустые метки, точку в конце, '_' и дефис на краю метки, битый punycode.
//     Этого мало: символы со статусом UTS #46 «valid, NV8» (пустой символ Брайля U+2800, эмодзи),
//     которые IDNA2008 запрещает, и киданьский заполнитель U+16FE4 профиль пропускает. Поэтому
//     A-label декодируется обратно (ToUnicode), и U-форма проходит белый список domainRuneOK —
//     один путь проверки и для введённого U-label, и для готового «xn--…».
//   - Общий предел 254 байта — у хранимой ASCII-формы: A-label длиннее исходного написания.
//
// Политика — решение пользователя 08.10.2026 (спека staff-admin-geo2 §4.1).
//
// Смешение письменностей в домене (кириллическая «а» в латинском имени) UTS #46 не запрещает:
// такой домен принимается, но его A-label отличается от латинского, поэтому двойником
// существующей учётки он не становится. ok=false — не адрес.
func NormalizeEmail(s string) (string, bool) {
	if len(s) > maxEmailInput {
		return "", false
	}
	e := strings.ToLower(strings.TrimSpace(norm.NFKC.String(s)))
	local, host, found := strings.Cut(e, "@")
	if !found || local == "" || host == "" || strings.Contains(host, "@") {
		return "", false
	}
	if len(local) > maxLocalLen || !isDotAtom(local) {
		return "", false
	}
	ascii, err := idna.Registration.ToASCII(host)
	if err != nil {
		return "", false
	}
	ascii = strings.ToLower(ascii)
	uform, err := idna.Registration.ToUnicode(ascii)
	if err != nil {
		return "", false
	}
	for _, r := range uform {
		if !domainRuneOK(r) {
			return "", false
		}
	}
	out := local + "@" + ascii
	// страховка: локальная часть уже проверена (isDotAtom), A-label — ASCII по построению
	if len(out) > maxEmailLen || !isPrintableASCII(out) {
		return "", false
	}
	return out, true
}

// isDotAtom — dot-atom-text RFC 5322 (§3.2.3): atext, разделённые одиночными точками; точка не
// первая и не последняя. Только ASCII.
func isDotAtom(s string) bool {
	if s == "" || s[0] == '.' || s[len(s)-1] == '.' || strings.Contains(s, "..") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.':
		case strings.IndexByte(atextSpecials, c) >= 0:
		default:
			return false
		}
	}
	return true
}

// domainRuneOK — символ декодированного домена (U-формы): буква, неохватывающая метка (Mn, Mc),
// десятичная цифра, '-' или '.', и не невидимый. IDNA2008 (RFC 5892, LetterDigits) допускает только
// Mn и Mc: охватывающие метки Me (U+20DD, U+20E3, U+0488) дорисовывают символ вокруг буквы, а профиль
// регистрации UTS #46 их пропускает. Символы категорий S и P (U+2800, эмодзи) IDNA2008 тоже запрещает.
func domainRuneOK(r rune) bool {
	if !unicode.IsLetter(r) && !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Mc, r) &&
		!unicode.Is(unicode.Nd, r) && r != '-' && r != '.' {
		return false
	}
	return !isInvisible(r)
}

// isInvisible — невидимый или «пустой» символ, который делает домен двойником на вид.
// Проверка — надмножество Default_Ignorable_Code_Point в той части, что доступна из stdlib: самого
// свойства Default_Ignorable_Code_Point в unicode нет, поэтому берём его основную часть
// Other_Default_Ignorable_Code_Point (CGJ, хангыльские заполнители U+115F, U+1160, U+3164, U+FFA0),
// Variation_Selector и всю категорию Cf (она шире DICP). Сверх неё — явный список «пустых» символов,
// которые в Default_Ignorable не входят, а профиль регистрации UTS #46 пропускает или может пропустить.
func isInvisible(r rune) bool {
	if unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r) ||
		unicode.Is(unicode.Cf, r) {
		return true
	}
	switch r {
	case 0x115F, 0x1160, 0x3164, 0xFFA0, // хангыльские заполнители (дублируют Other_Default_Ignorable)
		0x16FE4, // KHITAN SMALL SCRIPT FILLER — Mn, не Default_Ignorable
		0x2800:  // BRAILLE PATTERN BLANK — So, «valid, NV8» в UTS #46
		return true
	}
	return false
}

// isPrintableASCII — только печатные ASCII-символы 0x21–0x7E: без пробела, управляющих и не-ASCII.
func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return true
}
