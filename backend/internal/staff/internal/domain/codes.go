package domain

import (
	"fmt"
	"io"

	"wf/backend/internal/platform/otp"
)

// NewInviteCode — код приглашения вида "xxxxx-xxxxx" (решение пользователя 14.10.2026; otp.NewCode — общий
// генератор с резервными кодами): 50 бит, перебор под IP-лимитом недостижим за жизнь приглашения (24 ч),
// поэтому неверные вводы код не стирают. В базе — только шифротекст показанной формы (keys.Ring), сверка —
// по NormalizeInviteCode.
func NewInviteCode(rnd io.Reader) (string, error) {
	c, err := otp.NewCode(rnd)
	if err != nil {
		return "", fmt.Errorf("domain: код приглашения: %w", err)
	}
	return c, nil
}

// NewRecoveryCodes — 10 резервных кодов вида "xxxxx-xxxxx" (otp.NewRecoveryCodes). В базу идёт HMAC
// нормализованного кода с меткой staff-recovery (app.recoveryMAC), не сам код.
func NewRecoveryCodes(rnd io.Reader) ([]string, error) { return otp.NewRecoveryCodes(rnd) }

// NormalizeRecoveryCode — резервный код в каноническом виде (otp.NormalizeRecoveryCode).
func NormalizeRecoveryCode(s string) (string, bool) { return otp.NormalizeRecoveryCode(s) }

// NormalizeInviteCode — код приглашения в каноническом виде: та же нормализация, что у резервных кодов.
func NormalizeInviteCode(s string) (string, bool) { return otp.NormalizeRecoveryCode(s) }

// NormalizeCode — 6 цифр кода TOTP (otp.NormalizeCode).
func NormalizeCode(s string) (string, bool) { return otp.NormalizeCode(s) }
