package domain

import (
	"errors"

	"wf/backend/internal/platform/password"
)

// PasswordWeakReason — причина identity.password_weak (reasons) по ошибке password.Check: too_short, common,
// too_long; ok=false — ошибка не о слабости пароля (или nil).
func PasswordWeakReason(err error) (string, bool) {
	switch {
	case errors.Is(err, password.ErrTooShort):
		return "too_short", true
	case errors.Is(err, password.ErrCommon):
		return "common", true
	case errors.Is(err, password.ErrTooLong):
		return "too_long", true
	}
	return "", false
}
