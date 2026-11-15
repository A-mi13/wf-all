package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
)

// Logout — выход из текущей сессии (спека §3.7; Task 11): revoked_reason = logout, событие sessions_revoked и
// версия аккаунта (revokeSession: lockUser первым, R30). Сессии нет или она уже отозвана — nil (идемпотентно, 204).
// Ровно одна транзакция — под Idempotency-Key ручки.
func (a *App) Logout(ctx context.Context, sessionID uuid.UUID) error {
	userID, err := identitydb.New(a.pool).SessionUser(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		userID = uuid.Nil
	} else if err != nil {
		return fmt.Errorf("identity: сессия: %w", err)
	}
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if userID == uuid.Nil {
			return nil // транзакция всё равно одна: ключ идемпотентности сохраняет 204
		}
		_, err := a.revokeSession(ctx, tx, userID, sessionID, domain.RevokedLogout)
		if errors.Is(err, errUserGone) {
			return nil
		}
		return err
	})
}
