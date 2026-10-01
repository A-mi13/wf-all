package audit

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Ключи с персональными данными и секретами (§6.9: в before/after ПД маскируются). Ключ
// совпадает целиком или оканчивается на _<имя> (contact_email, refresh_token).
var sensitive = []string{"email", "phone", "birth_date", "password", "password_hash",
	"secret", "totp_secret", "token", "recovery_codes"}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitive {
		if k == s || strings.HasSuffix(k, "_"+s) {
			return true
		}
	}
	return false
}

// Mask сериализует v в JSON и маскирует значения чувствительных ключей на любой глубине.
// nil — nil (NULL в базе).
func Mask(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
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
			if isSensitive(k) {
				t[k] = maskValue(k, val)
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

func maskValue(key string, v any) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return "***"
	}
	k := strings.ToLower(key)
	switch {
	case k == "email" || strings.HasSuffix(k, "_email"):
		local, domain, found := strings.Cut(s, "@")
		if !found || local == "" {
			return "***"
		}
		return string([]rune(local)[0]) + "***@" + domain
	case k == "phone" || strings.HasSuffix(k, "_phone"):
		if len(s) < 4 {
			return "***"
		}
		return "***" + s[len(s)-2:]
	default:
		return "***"
	}
}
