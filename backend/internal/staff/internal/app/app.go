// Package app — сценарии модуля staff (спека сотрудников §4): вход, второй фактор, блокировка,
// загрузчик сессии admin-api, составители писем. Импортирует корень staff (ошибки, DTO, события) и
// платформу; таблицы — только staff_* через staffdb. Транзакции — db.InTx, ровно одна закоммиченная
// на сценарий под Idempotency-Key; неудача второго фактора пишется в транзакции проверки под замком
// учётки и коммитится до ответа 4xx (D8, withFactor).
package app

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/staff"
)

// Сроки и пороги (спека §4.6–4.7, global-constraints).
const (
	mfaPendingTTL     = 5 * time.Minute  // сессия после пароля ждёт второй фактор
	sessionTTL        = 8 * time.Hour    // абсолютный срок сессии от входа (и стадии setup)
	idleTimeout       = 20 * time.Minute // бездействие дольше — Reauth
	touchEvery        = time.Minute      // last_seen_at — не чаще раза в минуту на сессию
	replacedGrace     = time.Minute      // старый токен после замены — «заменён», а не «невалиден»
	maxFactorFailures = 5                // неудач второго фактора подряд до блокировки
	lockDuration      = time.Hour        // блокировка учётки
	lockNotifyEvery   = time.Hour        // письмо staff.locked — не чаще раза в час на учётку
	detachedTimeout   = 15 * time.Second // срок записи неудачи после обрыва соединения клиента
)

// loginEmailPolicy — лимит входа по нормализованной почте (D7): 10 попыток за 15 минут, одинаково и
// для несуществующих адресов.
var loginEmailPolicy = ratelimit.Policy{Limit: 10, Period: 15 * time.Minute, Burst: 10}

// classStaffLogin — класс ключа лимита по почте (ratelimit.EmailKey).
const classStaffLogin = "staff_login"

// Метки HMAC кольца ключей (keys.Ring.MAC): у каждой — свой независимый ключ.
const (
	labelRecovery   = "staff-recovery"
	labelLoginEmail = "staff-login-email"
)

// Причины гашения сессии (CHECK staff_sessions_revoked_reason_check).
const (
	reasonLogout   = "logout"
	reasonExpired  = "expired"
	reasonLocked   = "locked"
	reasonReplaced = "replaced"
)

// Действия аудита и тип объекта.
const (
	actionLogin          = "staff.login"
	actionLoginFailed    = "staff.login_failed"
	actionFactorFailed   = "staff.factor_failed"
	actionLocked         = "staff.locked"
	actionUnlock         = "staff.unlock"
	methodTOTP           = "totp"
	methodRecovery       = "recovery"
	objectAccount        = "staff_account"
	roleAnonymous        = "anonymous"
	roleSystem           = "system"
	statusActive         = "active"
	statusDisabled       = "disabled"
	roleOwner, roleAdmin = "owner", "admin"
)

// Шаги второго фактора: в какой операции случилась неудача (аудит, D7). Task 6b/7 добавляют свои.
const (
	stepMFA    = "mfa"    // вход: шаг после пароля
	stepUnlock = "unlock" // разблокировка сессии после бездействия или смены сети
)

// passwordHasher — хэшер паролей сценариев (*password.Hasher; шов — страж выравнивания времени входа).
type passwordHasher interface {
	Hash(ctx context.Context, pw string) (string, error)
	Verify(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error)
	VerifyDummy(ctx context.Context, pw string) error
}

// App — сценарии staff.
type App struct {
	pool    *pgxpool.Pool
	clock   clock.Clock
	keys    *keys.Ring
	hasher  passwordHasher
	limiter ratelimit.Limiter
	river   *river.Client[pgx.Tx]
	rand    io.Reader
	origin  string
	locales *i18n.Catalog
	log     *slog.Logger
}

// New — сценарии на зависимостях admin-api.
func New(d staff.Deps) *App {
	return &App{
		pool: d.Pool, clock: d.Clock, keys: d.Keys, hasher: d.Hasher, limiter: d.Limiter, river: d.River,
		rand: d.Rand, origin: d.Origin, locales: d.Locales, log: d.Log,
	}
}
