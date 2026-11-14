package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// onSecondFactorLogin — вход владельца вторым фактором в tx finishLogin (R20): живой сброс 2FA поддержкой
// отменяется. Файл создаёт Task 9 пустым, тело — Task 18 (иначе 16 и 18 правили бы один файл).
func (a *App) onSecondFactorLogin(_ context.Context, _ pgx.Tx, _ uuid.UUID) error {
	return nil
}
