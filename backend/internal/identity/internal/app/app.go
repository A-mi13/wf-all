// Package app — сценарии модуля identity (спека identity §3): регистрация и вход, коды из письма, сессии и устройства,
// 2FA, свежий вход, удаление, сброс 2FA поддержкой, чистка. Импортирует корень identity (Deps, коды, события) и
// платформу; таблицы — только свои через identitydb. Типы app — свои: oapi здесь не импортируется (только в httpapi).
//
// Транзакции — db.InTx, ровно одна закоммиченная на сценарий под Idempotency-Key; неудача (попытки, счётчики,
// письма) коммитится до ответа ошибкой (R6). argon2 — вне транзакции (R31).
//
// Порядок замков (R30, I2): users → sessions → refresh_tokens → devices → user_mfa/user_recovery_codes → mfa_resets →
// auth_codes/auth_challenges. Сценарий, меняющий аккаунт или его сессии, первым берёт users FOR NO KEY UPDATE
// (lockUser). Аккаунт, известный только из строки кода, вызова или токена, — приём lockByRow: строка без замка →
// lockUser(user_id) → FOR UPDATE строки с перепроверкой. Строка approvals (платформа) — раньше строк модуля.
// Исключение из порядка (m7 ревью W4-А): сценарии кода (сброс пароля tx1/tx2, смена почты, регистрация) берут строку
// auth_codes сразу после users и лишь затем user_mfa, sessions, devices; так же второй шаг входа (completeMfa) и
// отмена удаления берут строку auth_challenges (lockByRow) раньше всех прочих строк аккаунта. Взаимной
// блокировки нет: всякий сценарий, трогающий строки одного аккаунта, сперва держит его users, и второй ждёт на
// users, не доходя до остальных строк;
// коды без аккаунта (регистрация, приманка) других строк модуля под замком не берут. Старты кода на (назначение,
// почта) идут по очереди под pg_advisory_xact_lock (startCode), после users, если он взят.
// Внутри db.InTx — только соединение tx: лимит — a.limiterTx(tx), не a.limiter/a.risk/a.pool (второе соединение
// под замком users при исчерпанном пуле ждёт вечно; C1, I1 ревью W4-А).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/ipgeo"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/token"
)

// App — сценарии identity. Собирается New (api), NewAdmin (admin-api), NewTx (identity/intx) или NewMaintenance
// (воркер, rekey).
type App struct {
	pool       *pgxpool.Pool
	clock      clock.Clock
	secrets    *keys.Ring // NewMaintenance без кольца — nil (admin-api, N3)
	devices    *keys.Ring
	hasher     identity.PasswordHasher
	limiter    ratelimit.Limiter
	limiterTx  func(pgx.Tx) ratelimit.Limiter // лимит внутри db.InTx — только им (C1 волны 4)
	risk       *risk.Assessor
	human      humancheck.Verifier
	geo        geo.Service
	flags      *flags.Store
	ipgeo      ipgeo.Locator
	legal      identity.LegalDocs
	tokens     *token.Issuer
	river      *river.Client[pgx.Tx]
	attestor   identity.Attestor
	providers  identity.ProviderReadiness
	totpIssuer string
	log        *slog.Logger
	hooks      *hooks // точки синхронизации тестов; nil в проде
	// captainChecks — порты условий капитанства (New, NewAdmin; ровно RequiredCaptainChecks)
	captainChecks []identity.CaptainCheck
	// staffLinks — текущая связь сотрудника с игроком в tx решения (NewAdmin, R38)
	staffLinks identity.StaffLinks
}

// hooks — точки синхронизации тестов пакета app (N1, N6). Ставит только тест; вызовы — владельцы сценариев.
type hooks struct {
	// afterRowFound — строка кода, вызова или токена найдена без замка, до lockUser (kind — вид строки).
	afterRowFound func(kind string)
	// afterFirstLock — после первого оператора с замком сценария (не внутри lockUser, R3-5); место вызова
	// выбирает сценарий: подача заявки — после замка аккаунта и правила условий (opSubmitCaptainApplication).
	afterFirstLock func(op string)
}

func (a *App) rowFound(kind string) {
	if a.hooks != nil && a.hooks.afterRowFound != nil {
		a.hooks.afterRowFound(kind)
	}
}

func (a *App) firstLock(op string) {
	if a.hooks != nil && a.hooks.afterFirstLock != nil {
		a.hooks.afterFirstLock(op)
	}
}

// errMissingDeps — забытая зависимость сборки: отказ старта, а не паника в запросе.
var errMissingDeps = errors.New("identity: не заданы зависимости")

// New — сценарии на зависимостях api: каждая обязательна.
func New(d identity.Deps) (*App, error) {
	var missing []string
	for name, ok := range map[string]bool{
		"Pool": d.Pool != nil, "Clock": d.Clock != nil, "Secrets": d.Secrets != nil, "Devices": d.Devices != nil,
		"Hasher": d.Hasher != nil, "Limiter": d.Limiter != nil, "LimiterTx": d.LimiterTx != nil, "Risk": d.Risk != nil, "Human": d.Human != nil,
		"Geo": d.Geo != nil, "Flags": d.Flags != nil, "IPGeo": d.IPGeo != nil, "Tokens": d.Tokens != nil,
		"River": d.River != nil, "Attestor": d.Attestor != nil, "Providers": d.Providers != nil,
		"TOTPIssuer": d.TOTPIssuer != "", "Log": d.Log != nil,
		// нулевое LegalDocs не годно: его даёт только ParseLegalDocuments
		"Legal": len(d.Legal.Items()) > 0 && d.Legal.Items()[0].Version != "",
	} {
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("%w: %v", errMissingDeps, missing)
	}
	if err := identity.ValidateCaptainChecks(d.CaptainChecks); err != nil {
		return nil, err
	}
	return &App{
		pool: d.Pool, clock: d.Clock, secrets: d.Secrets, devices: d.Devices, hasher: d.Hasher, limiter: d.Limiter,
		limiterTx: d.LimiterTx,
		risk:      d.Risk, human: d.Human, geo: d.Geo, flags: d.Flags, ipgeo: d.IPGeo, legal: d.Legal, tokens: d.Tokens,
		river: d.River, attestor: d.Attestor, providers: d.Providers, totpIssuer: d.TOTPIssuer, log: d.Log,
		captainChecks: slices.Clone(d.CaptainChecks),
	}, nil
}

// NewAdmin — сценарии admin-api (план I-4, Task 2): заявки в капитаны, поиск игрока, статистика способов входа и
// сброс 2FA поддержкой. Обязательны Pool, Clock, River, Geo, StaffLinks, Log; порты условий — ровно
// RequiredCaptainChecks (иначе отказ старта, К1). Кольца identity у admin-api нет (N3): secrets nil.
func NewAdmin(d identity.AdminDeps) (*App, error) {
	var missing []string
	for name, ok := range map[string]bool{
		"Pool": d.Pool != nil, "Clock": d.Clock != nil, "River": d.River != nil, "Geo": d.Geo != nil,
		"StaffLinks": d.StaffLinks != nil, "Log": d.Log != nil,
	} {
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf("%w: NewAdmin %v", errMissingDeps, missing)
	}
	if err := identity.ValidateCaptainChecks(d.CaptainChecks); err != nil {
		return nil, err
	}
	return &App{pool: d.Pool, clock: d.Clock, river: d.River, geo: d.Geo, log: d.Log, ipgeo: ipgeo.None,
		captainChecks: slices.Clone(d.CaptainChecks), staffLinks: d.StaffLinks}, nil
}

// TxDeps — зависимости сценариев identity/intx (методы в транзакции вызывающего модуля): без портов условий — intx
// правило капитанства не считает (I7в ревью плана I-4).
type TxDeps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	River *river.Client[pgx.Tx]
	Geo   geo.Service
	Log   *slog.Logger
}

// NewTx — сценарии для identity/intx; все зависимости обязательны.
func NewTx(d TxDeps) (*App, error) {
	if d.Pool == nil || d.Clock == nil || d.River == nil || d.Geo == nil || d.Log == nil {
		return nil, fmt.Errorf("%w: NewTx — Pool, Clock, River, Geo, Log", errMissingDeps)
	}
	return &App{pool: d.Pool, clock: d.Clock, river: d.River, geo: d.Geo, log: d.Log, ipgeo: ipgeo.None}, nil
}

// NewMaintenance — сценарии воркера, admin-api и rekey (анонимизация, исполнение и назначение сброса 2FA, чистка,
// письма): хэшер, токены и geo не нужны. Обязательны pool, clock, river, log; secrets — нет (N3): admin-api кольца
// identity не получает, а запись журнала с адресом без кольца — errNoSecrets.
func NewMaintenance(pool *pgxpool.Pool, c clock.Clock, secrets *keys.Ring, rc *river.Client[pgx.Tx], log *slog.Logger) (*App, error) {
	if pool == nil || c == nil || rc == nil || log == nil {
		return nil, fmt.Errorf("%w: NewMaintenance — Pool, Clock, River, Log", errMissingDeps)
	}
	return &App{pool: pool, clock: c, secrets: secrets, river: rc, log: log, ipgeo: ipgeo.None}, nil
}

// userRow — строка аккаунта под замком.
type userRow = identitydb.User

// errUserGone — аккаунта под замком нет (удалён между поиском и замком) — вызывающий отвечает как «истёк».
var errUserGone = errors.New("identity: аккаунта нет")

// lockUser — первый замок сценария над аккаунтом: users FOR NO KEY UPDATE (R30). Повторный вызов в той же tx —
// без ожидания.
func (a *App) lockUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (userRow, error) {
	u, err := identitydb.New(tx).LockUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return userRow{}, errUserGone
	}
	if err != nil {
		return userRow{}, fmt.Errorf("identity: замок аккаунта: %w", err)
	}
	return u, nil
}

// errRowGone — строка кода, вызова или токена исчезла или сменила владельца между поиском и замком: ответ
// «истёк» операции (код, mfa_token, restore_token, refresh).
var errRowGone = errors.New("identity: строка исчезла или сменила аккаунт")

// lockByRow — приём R30 «аккаунт по строке» (N1): find ищет строку без замка и отдаёт её user_id (nil — строка
// без аккаунта: код регистрации, приманка сброса); hooks.afterRowFound(kind); lockUser(user_id), а у строки без аккаунта —
// тот же запрос замка вхолостую по нулевому id (§9.1: приманка и настоящий код делают одну работу с базой, страж
// TestCodeDecoySameWork; строки с таким id нет, замка нет);
// lock берёт ту же строку FOR UPDATE и отдаёт её user_id — другой — errRowGone. Перепроверку состояния строки
// (погашена, отозвана, истекла) делает lock вызывающего. Ошибку find (pgx.ErrNoRows) вызывающий видит как есть.
func (a *App) lockByRow(ctx context.Context, tx pgx.Tx, kind string,
	find func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error),
	lock func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error)) error {
	q := identitydb.New(tx)
	before, err := find(ctx, q)
	if err != nil {
		return err
	}
	a.rowFound(kind)
	if before != nil {
		if _, err := a.lockUser(ctx, tx, *before); err != nil {
			if errors.Is(err, errUserGone) {
				return errRowGone
			}
			return err
		}
	} else if _, err := a.lockUser(ctx, tx, uuid.Nil); err != nil && !errors.Is(err, errUserGone) {
		return err
	}
	after, err := lock(ctx, q)
	if errors.Is(err, pgx.ErrNoRows) {
		return errRowGone
	}
	if err != nil {
		return err
	}
	if (before == nil) != (after == nil) || (before != nil && *before != *after) {
		return errRowGone
	}
	return nil
}
