package audit_test

import (
	"encoding/json"
	"testing"

	"wf/backend/internal/platform/audit"
)

func TestMaskHidesPersonalData(t *testing.T) {
	in := map[string]any{
		"nickname":      "Kolya",
		"email":         "ivan.petrov@mail.ru",
		"contact_phone": "+79181234567",
		"birth_date":    "2008-05-01",
		"password_hash": "$argon2id$...",
		"profile": map[string]any{
			"email": "x@y.ru",
			"tags":  []any{map[string]any{"refresh_token": "abc"}},
		},
		"phone": 79181234567,
	}
	raw, err := audit.Mask(in)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"nickname":      "Kolya",
		"email":         "i***@mail.ru",
		"contact_phone": "***67",
		"birth_date":    "***",
		"password_hash": "***",
		"phone":         "***",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, ждали %v", k, got[k], v)
		}
	}
	profile := got["profile"].(map[string]any)
	if profile["email"] != "x***@y.ru" {
		t.Errorf("вложенная почта: %v", profile["email"])
	}
	if tok := profile["tags"].([]any)[0].(map[string]any)["refresh_token"]; tok != "***" {
		t.Errorf("токен в массиве: %v", tok)
	}
}

func TestMaskNilIsNull(t *testing.T) {
	raw, err := audit.Mask(nil)
	if err != nil || raw != nil {
		t.Fatalf("Mask(nil) = %s, %v", raw, err)
	}
}

func maskMap(t *testing.T, in any) map[string]any {
	t.Helper()
	raw, err := audit.Mask(in)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// Структура без json-тегов сериализуется ключами PascalCase — они маскируются так же.
func TestMaskStructWithoutJSONTags(t *testing.T) {
	type user struct {
		Nickname     string
		PasswordHash string
		ContactEmail string
		BirthDate    string
		RefreshToken string
		IPAddress    string
	}
	got := maskMap(t, user{"Kolya", "$argon2id$x", "ivan@mail.ru", "2008-05-01", "abc", "203.0.113.7"})
	want := map[string]any{
		"Nickname": "Kolya", "PasswordHash": "***", "ContactEmail": "i***@mail.ru",
		"BirthDate": "***", "RefreshToken": "***", "IPAddress": "***",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, ждали %v", k, got[k], v)
		}
	}
}

func TestMaskKeyRules(t *testing.T) {
	const mail, phone = "ivan@mail.ru", "+79181234567"
	cases := []struct {
		key  string
		in   any
		want any
	}{
		// camelCase из JSON клиентов
		{"contactEmail", mail, "i***@mail.ru"},
		{"birthDate", "2008-05-01", "***"},
		{"refreshToken", "abc", "***"},
		{"passwordHash", "x", "***"},
		{"recoveryCodes", "x", "***"},
		// колонки сессий
		{"refresh_hash", "x", "***"},
		{"ip", "203.0.113.7", "***"},
		{"user_agent", "Mozilla/5.0", "***"},
		{"device_name", "iPhone Ивана", "***"},
		{"device_id", "d-1", "***"},
		// частые формы
		{"phone_number", phone, "***67"},
		{"email_address", mail, "i***@mail.ru"},
		{"mobile", phone, "***67"},
		{"msisdn", phone, "***67"},
		{"emails", mail, "i***@mail.ru"},
		{"emails", []any{mail}, "***"},
		{"phones", []any{phone}, "***"},
		{"dob", "2008-05-01", "***"},
		{"date_of_birth", "2008-05-01", "***"},
		{"birthday", "2008-05-01", "***"},
		{"first_name", "Иван", "***"},
		{"last_name", "Петров", "***"},
		{"middle_name", "Иванович", "***"},
		{"full_name", "Иван Петров", "***"},
		{"patronymic", "Иванович", "***"},
		{"api_key", "k", "***"},
		{"otp", "123456", "***"},
		{"totp", "123456", "***"},
		{"code_hash", "x", "***"},
		{"passwd", "x", "***"},
		{"secrets", "x", "***"},
		{"tokens", "x", "***"},
		{"recovery_code", "x", "***"},
		{"provider_user_id", "u-1", "***"},
		{"telegram_id", "123", "***"},
		{"vk_id", "123", "***"},
		{"phone", 79181234567, "***"},
		{"email", "", "***"},
		// не чувствительное: метки времени, флаги, счётчики и обычные поля
		{"email_verified_at", "2026-10-01T10:00:00Z", "2026-10-01T10:00:00Z"},
		{"phone_verified", true, true},
		{"email_verified", false, false},
		{"password_changed_at", "2026-10-01T10:00:00Z", "2026-10-01T10:00:00Z"},
		{"token_count", 3, 3},
		{"nickname", "Kolya", "Kolya"},
		{"address", "ул. Мира, 1", "ул. Мира, 1"},
		{"city_id", "c-1", "c-1"},
		{"last_seen_at", "2026-10-01T10:00:00Z", "2026-10-01T10:00:00Z"},
		{"created_at", "2026-10-01T10:00:00Z", "2026-10-01T10:00:00Z"},
		{"status", "active", "active"},
	}
	for _, c := range cases {
		got := maskMap(t, map[string]any{c.key: c.in})[c.key]
		if !equalJSON(got, c.want) {
			t.Errorf("%s: %v -> %v, ждали %v", c.key, c.in, got, c.want)
		}
	}
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestMaskKeepsCreatedAtInSessionsLikeRow(t *testing.T) {
	got := maskMap(t, map[string]any{
		"refresh_hash": "h", "ip": "203.0.113.7", "user_agent": "UA", "device_name": "Pixel",
		"created_at": "2026-10-01T10:00:00Z",
	})
	for _, k := range []string{"refresh_hash", "ip", "user_agent", "device_name"} {
		if got[k] != "***" {
			t.Errorf("%s = %v", k, got[k])
		}
	}
	if got["created_at"] != "2026-10-01T10:00:00Z" {
		t.Errorf("created_at замаскирован: %v", got["created_at"])
	}
}

// Типизированный nil даёт JSON null — это SQL NULL, а не jsonb 'null'.
func TestMaskTypedNilIsNull(t *testing.T) {
	var p *struct{ A int }
	var m map[string]any
	for name, v := range map[string]any{"указатель": p, "map": m} {
		raw, err := audit.Mask(v)
		if err != nil || raw != nil {
			t.Errorf("%s: Mask = %s, %v", name, raw, err)
		}
	}
}
