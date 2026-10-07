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
		"#1babygirl":                   password.ErrCommon, // запись списка с «#» — пароль, не комментарий
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

// Длина и список — после NFKC, как пароль и будет хэширован.
func TestCheckNFKC(t *testing.T) {
	cases := map[string]error{
		"ｑｗｅｒｔｙｕｉｏｐ":                                  password.ErrCommon,   // полноширинный qwertyuiop
		strings.Repeat("e\u0301", 9):                  password.ErrTooShort, // 18 кодовых точек, 9 символов
		strings.Repeat("e\u0301", 10):                 nil,
		strings.Repeat("\ufb00", 65):                  password.ErrTooLong, // ﬀ → ff: 130 символов
		strings.Repeat("\ufb00", 64):                  nil,
		strings.Repeat("a", 1<<20):                    password.ErrTooLong, // до NFKC, по байтам
		strings.Repeat("a", password.MaxInputBytes+1): password.ErrTooLong,
	}
	for pw, want := range cases {
		if got := password.Check(pw); !errors.Is(got, want) {
			t.Errorf("Check(%.20q…) = %v, нужно %v", pw, got, want)
		}
	}
}
