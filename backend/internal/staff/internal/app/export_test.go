package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Для тестов: секрет TOTP и резервные коды тем же путём, что у выдачи (Task 7).

func (a *App) SealTOTP(staffID uuid.UUID, secret []byte) ([]byte, string, error) {
	return a.sealTOTP(staffID, secret)
}

func (a *App) RecoveryMAC(keyID string, staffID uuid.UUID, norm string) ([]byte, error) {
	return a.recoveryMAC(keyID, staffID, norm)
}

func (a *App) LoginEmailKey(email string) (string, error) { return a.loginEmailKey(email) }

// Сроки — тестам блокировки и сессии.
const (
	MaxFactorFailures = maxFactorFailures
	ReplacedGrace     = replacedGrace
)

// PasswordHasher — шов хэшера паролей (страж выравнивания времени входа).
type PasswordHasher = passwordHasher

func (a *App) SetHasher(h PasswordHasher) { a.hasher = h }

// FactorWith — проверка через withFactor, где check без проверки сессии сразу отвечает видом fail
// (страж: withFactor засчитывает только известные виды неудачи).
func (a *App) FactorWith(ctx context.Context, s *auth.Staff, step string, m staff.Meta, fail error) error {
	return a.withFactor(ctx, s, step, m,
		func(context.Context, pgx.Tx, *staffdb.Queries, staffdb.LockAccountRow, time.Time) (error, error) {
			return fail, nil
		})
}

// FailFactor — неудачная проверка через withFactor (путь 6b/7: смена пароля, перевыпуск кодов).
func (a *App) FailFactor(ctx context.Context, s *auth.Staff, step string, m staff.Meta) error {
	return a.withFactor(ctx, s, step, m,
		func(context.Context, pgx.Tx, *staffdb.Queries, staffdb.LockAccountRow, time.Time) (error, error) {
			return staff.ErrPasswordInvalid, nil
		})
}
