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
