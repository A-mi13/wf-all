package password

import (
	_ "embed"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	MinLength = 10  // NIST SP 800-63B: длина вместо правил состава (§7.1)
	MaxLength = 128 // защита от гигантских паролей; argon2 съел бы и их, но смысла нет
)

var (
	ErrTooShort = errors.New("password: короче 10 символов")
	ErrTooLong  = errors.New("password: длиннее 128 символов")
	ErrCommon   = errors.New("password: из списка распространённых")
)

//go:embed common.txt
var commonRaw string

var commonSet = sync.OnceValue(func() map[string]struct{} {
	// Комментарий — только строка с «# »: записи списка вроде «#1babygirl» — настоящие пароли.
	set := make(map[string]struct{}, 10000)
	for line := range strings.SplitSeq(commonRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		set[line] = struct{}{}
	}
	return set
})

// Check — правила пароля без правил состава: длина в символах и список распространённых
// (без учёта регистра).
func Check(pw string) error {
	switch n := utf8.RuneCountInString(pw); {
	case n < MinLength:
		return ErrTooShort
	case n > MaxLength:
		return ErrTooLong
	}
	if _, ok := commonSet()[strings.ToLower(pw)]; ok {
		return ErrCommon
	}
	return nil
}
