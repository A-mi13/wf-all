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

// CreateInvited — помощник 8b: учётка invited с ролью и городами; createdBy uuid.Nil — оператор.
func (a *App) CreateInvited(ctx context.Context, tx pgx.Tx, email, name, role string, cities []uuid.UUID, createdBy uuid.UUID) (uuid.UUID, error) {
	inv, err := a.createInvited(ctx, staffdb.New(tx), newAccount{
		email: email, name: name, role: role, cities: cities, by: createdBy,
	}, a.clock.Now())
	return inv.id, err
}

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

// InitiatorValid — предикат проверки инициатора (D17, I1): статус, закрытость, версия и право по роли.
func InitiatorValid(status, role string, version int64, lockedUntil *time.Time, initID uuid.UUID, initVer int64, permission string, now time.Time) bool {
	return initiatorValid(status, role, version, lockedUntil, payloadBase{V: 1, InitID: initID, InitVer: initVer}, permission, uuid.Nil, now)
}

// OwnersLockKey — ключ замка стража последнего владельца (тест держит его, чтобы выстроить очередь).
const OwnersLockKey = ownersLockKey

// SetCleanupBatch — размер порции чистки на время теста; возвращает восстановление.
func SetCleanupBatch(n int32) func() {
	old := cleanupBatch
	cleanupBatch = n
	return func() { cleanupBatch = old }
}
