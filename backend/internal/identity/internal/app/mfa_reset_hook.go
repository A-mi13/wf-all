package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// onSecondFactorLogin — вход владельца вторым фактором в tx finishLogin (R20, спека §3.15 п. 4): владелец доказал, что
// факторы при нём, — живой сброс 2FA поддержкой отменяется (cancelled_by = owner, событие, письмо, аудит). users уже
// под замком finishLogin. Ошибка — откат входа (500), не тихий пропуск.
func (a *App) onSecondFactorLogin(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := a.cancelResetsByOwner(ctx, tx, userID)
	return err
}
