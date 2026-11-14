package app

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Удаление аккаунта, отмена, анонимизация (спека §3.11) — владелец Task 15. Сигнатуры — Task 8 (I9), тело —
// errNotImplemented; владелец может вынести AnonymizeDue в anonymize.go.

// DeleteAccount — запрос удаления с периодом отмены.
func (a *App) DeleteAccount(_ context.Context, _ uuid.UUID, _ string) (purgeAt time.Time, err error) {
	return time.Time{}, errNotImplemented
}

// CancelDeletion — отмена удаления по restore_token.
func (a *App) CancelDeletion(_ context.Context, _ string, _ Client) (LoginOutcome, error) {
	return LoginOutcome{}, errNotImplemented
}

// AnonymizeDue — анонимизация аккаунтов с наступившим purge_at (воркер).
func (a *App) AnonymizeDue(_ context.Context, _ int) (n int, err error) {
	return 0, errNotImplemented
}
