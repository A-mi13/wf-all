package app

import (
	"context"

	"github.com/google/uuid"
)

// Сброс 2FA поддержкой: отмена владельцем и исполнение по сроку (спека §3.15) — владелец Task 18 (хук
// onSecondFactorLogin — mfa_reset_hook.go). Сигнатуры — Task 8 (I9), тело — errNotImplemented; владелец может
// вынести ExecuteDueMfaResets в mfa_reset_exec.go.

// CancelMyMfaReset — отмена сброса 2FA владельцем (m3).
func (a *App) CancelMyMfaReset(_ context.Context, _, _ uuid.UUID) error {
	return errNotImplemented
}

// ExecuteDueMfaResets — исполнение сбросов 2FA по сроку (воркер).
func (a *App) ExecuteDueMfaResets(_ context.Context, _ int) (int, error) {
	return 0, errNotImplemented
}
