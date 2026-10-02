package password_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"wf/backend/internal/platform/password"
)

func TestCheck(t *testing.T) {
	cases := map[string]error{
		"short":                        password.ErrTooShort,
		"девять!!!":                    password.ErrTooShort, // 9 символов, хотя байт больше 10
		"correct horse battery staple": nil,
		"мой длинный пароль":           nil,
		"1234567890":                   password.ErrCommon,
		"QWERTYUIOP":                   password.ErrCommon, // регистр не спасает
		"ЙЦУКЕНГШЩЗ":                   password.ErrCommon,
		"пароль1234":                   password.ErrCommon,
		strings.Repeat("я", 129):       password.ErrTooLong,
		strings.Repeat("я", 128):       nil,
	}
	for pw, want := range cases {
		if got := password.Check(pw); !errors.Is(got, want) {
			t.Errorf("Check(%q) = %v, нужно %v", pw, got, want)
		}
	}
}

// Список вшит целиком и соответствует правилу отбора: от 10 символов, нижний регистр.
func TestCommonListIntegrity(t *testing.T) {
	n := 0
	for _, line := range password.CommonList() {
		if utf8.RuneCountInString(line) < password.MinLength && len(line) < password.MinLength {
			t.Errorf("короткая строка %q", line)
		}
		if line != strings.ToLower(line) {
			t.Errorf("не нижний регистр: %q", line)
		}
		n++
	}
	if n < 9000 {
		t.Fatalf("в списке %d паролей — файл обрезан?", n)
	}
}
