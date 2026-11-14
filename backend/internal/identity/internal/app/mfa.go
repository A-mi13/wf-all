package app

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// 2FA игрока и второй шаг входа (спека §3.3) — владелец Task 16. Сигнатуры — Task 8 (I9), тело —
// errNotImplemented; владелец может вынести CompleteMfa в mfa_login.go.

// MfaStatusView — getMyMfa.
type MfaStatusView struct {
	TOTPEnabled       bool
	ConfirmedAt       *time.Time
	RecoveryCodesLeft int
	ResetScheduledAt  *time.Time
}

// MfaStatus — состояние 2FA.
func (a *App) MfaStatus(_ context.Context, _ uuid.UUID) (MfaStatusView, error) {
	return MfaStatusView{}, errNotImplemented
}

// SetupTotp — новый секрет (повтор выпускает новый).
func (a *App) SetupTotp(_ context.Context, _ uuid.UUID) (secret, uri string, err error) {
	return "", "", errNotImplemented
}

// ConfirmTotp — включение 2FA, 10 резервных кодов.
func (a *App) ConfirmTotp(_ context.Context, _ uuid.UUID, _ string) ([]string, error) {
	return nil, errNotImplemented
}

// DisableTotp — выключение при свежем входе вторым фактором (R27).
func (a *App) DisableTotp(_ context.Context, _ uuid.UUID) error {
	return errNotImplemented
}

// RegenerateRecoveryCodes — новые 10 кодов.
func (a *App) RegenerateRecoveryCodes(_ context.Context, _ uuid.UUID) ([]string, error) {
	return nil, errNotImplemented
}

// PasskeyInput — утверждение WebAuthn второго шага (I-3; в I-2 — passkey_invalid, R3).
type PasskeyInput struct {
	Credential []byte
}

// CompleteMfa — второй шаг входа по mfa_token.
func (a *App) CompleteMfa(_ context.Context, _ string, _ SecondFactor, _ *PasskeyInput, _ Client) (LoginOutcome, error) {
	return LoginOutcome{}, errNotImplemented
}
