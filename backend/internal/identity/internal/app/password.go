package app

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Сброс и смена пароля, свежий вход, смена почты (спека §3.4, §3.9, §3.10, §3.13) — владелец Task 13. Сигнатуры —
// Task 8 (I9), тело — errNotImplemented; владелец может разнести их по reauth_op.go и email_change.go.

// ResetInput — confirmPasswordReset.
type ResetInput struct {
	CodeID            uuid.UUID
	Code, NewPassword string
	MFA               *SecondFactor
}

// StartPasswordReset — код или приманка без письма, ответ одинаков.
func (a *App) StartPasswordReset(_ context.Context, _ string) (CodeSent, error) {
	return CodeSent{}, errNotImplemented
}

// ConfirmPasswordReset — две tx, сверка кода раньше ветвей 2FA (C1, N10).
func (a *App) ConfirmPasswordReset(_ context.Context, _ ResetInput, _ Client) (LoginOutcome, error) {
	return LoginOutcome{}, errNotImplemented
}

// ChangePassword — смена или установка пароля при свежем входе.
func (a *App) ChangePassword(_ context.Context, _ uuid.UUID, _ string) error {
	return errNotImplemented
}

// ReauthInput — reauthenticate: ровно одно поле (passkey и social в I-2 — reauth_invalid, R3).
type ReauthInput struct {
	Password, TOTPCode, RecoveryCode string
	Passkey, Social                  bool
}

// Reauthenticate — свежий вход; ответ — reauth_until.
func (a *App) Reauthenticate(_ context.Context, _ uuid.UUID, _ ReauthInput) (time.Time, error) {
	return time.Time{}, errNotImplemented
}

// StartEmailChange — код на новую почту или приманка.
func (a *App) StartEmailChange(_ context.Context, _ uuid.UUID, _ string) (CodeSent, error) {
	return CodeSent{}, errNotImplemented
}

// ConfirmEmailChange — новая почта по коду.
func (a *App) ConfirmEmailChange(_ context.Context, _, _ uuid.UUID, _ string) (MeView, error) {
	return MeView{}, errNotImplemented
}
