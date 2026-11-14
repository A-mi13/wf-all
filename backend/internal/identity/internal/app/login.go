package app

import (
	"context"

	"github.com/google/uuid"
)

// Вход, обновление токенов, выход (спека §3.2, §3.7) — владелец Task 11. Сигнатуры — Task 8 (I9), тело —
// errNotImplemented; владелец может разнести их по refresh.go и logout.go.

// Login — вход по почте и паролю.
func (a *App) Login(_ context.Context, _, _ string, _ Client) (LoginOutcome, error) {
	return LoginOutcome{}, errNotImplemented
}

// Refresh — ротация refresh с детектом повтора и окном гонки.
func (a *App) Refresh(_ context.Context, _, _ string, _ Client) (TokenPair, error) {
	return TokenPair{}, errNotImplemented
}

// Logout — выход из текущей сессии (идемпотентно).
func (a *App) Logout(_ context.Context, _ uuid.UUID) error {
	return errNotImplemented
}
