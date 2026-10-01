package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
)

// Правило ключа (§6.9: в before/after ПД и секреты маскируются). Ключ разбирается на слова:
// camelCase/PascalCase и разделители _ - . пробел дают один и тот же набор слов
// (ContactEmail, contactEmail, contact_email -> contact, email).

// Слова-исключения: если ключ ими оканчивается, это метка времени, счётчик или флаг, а не
// сами данные (email_verified_at, phone_verified, password_changed_at).
var safeLast = map[string]bool{"at": true, "count": true, "verified": true, "enabled": true, "required": true}

// Слова, делающие ключ чувствительным сами по себе.
var sensitiveWord = map[string]bool{
	"email": true, "emails": true, "phone": true, "phones": true, "mobile": true, "msisdn": true,
	"birth": true, "birthday": true, "dob": true, "patronymic": true,
	"password": true, "passwd": true, "secret": true, "secrets": true,
	"token": true, "tokens": true, "hash": true, "otp": true, "totp": true,
	"ip": true, "device": true, "telegram": true, "vk": true,
}

// Пары соседних слов: каждое слово в отдельности безобидно (name, user, key, code).
var sensitivePair = [][2]string{
	{"user", "agent"}, {"first", "name"}, {"last", "name"}, {"middle", "name"}, {"full", "name"},
	{"api", "key"}, {"recovery", "code"}, {"recovery", "codes"}, {"provider", "user"},
}

// words делит ключ на слова в нижнем регистре.
func words(key string) []string {
	rs := []rune(key)
	var b strings.Builder
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) {
			prev := rs[i-1]
			// lower->Upper (contactEmail) или конец акронима (IPAddress -> IP_Address).
			if unicode.IsLower(prev) || unicode.IsDigit(prev) ||
				(unicode.IsUpper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1])) {
				b.WriteRune('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.FieldsFunc(b.String(), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func isSensitive(ws []string) bool {
	if len(ws) == 0 || safeLast[ws[len(ws)-1]] {
		return false
	}
	for i, w := range ws {
		if sensitiveWord[w] {
			return true
		}
		for _, p := range sensitivePair {
			if w == p[0] && i+1 < len(ws) && ws[i+1] == p[1] {
				return true
			}
		}
	}
	return false
}

func hasWord(ws []string, set ...string) bool {
	for _, w := range ws {
		for _, s := range set {
			if w == s {
				return true
			}
		}
	}
	return false
}

// Mask сериализует v в JSON и маскирует значения чувствительных ключей на любой глубине.
// nil (в том числе типизированный nil-указатель и nil-map, дающие JSON null) — nil, то есть
// NULL в базе. Маскирование идёт по ключу, а не по содержимому: модули не должны класть
// в Before/After свободный текст с ПД (комментарии, описания) — он сохранится как есть,
// а аудит-лог только на вставку, исправить запись потом нельзя.
func Mask(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	return json.Marshal(walk(tree))
}

func walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			ws := words(k)
			if isSensitive(ws) {
				t[k] = maskValue(ws, val)
			} else {
				t[k] = walk(val)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = walk(t[i])
		}
		return t
	default:
		return v
	}
}

func maskValue(ws []string, v any) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return "***"
	}
	switch {
	case hasWord(ws, "email", "emails"):
		local, domain, found := strings.Cut(s, "@")
		if !found || local == "" {
			return "***"
		}
		return string([]rune(local)[0]) + "***@" + domain
	case hasWord(ws, "phone", "phones", "mobile", "msisdn"):
		if len(s) < 4 {
			return "***"
		}
		return "***" + s[len(s)-2:]
	default:
		return "***"
	}
}
