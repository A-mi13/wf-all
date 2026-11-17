// Package intx — операции identity в транзакции вызывающего модуля (спека identity §10.2, спека бэкенда §4.4):
// RevokeRoleInTx (teams, moderation), SetRestrictionsInTx (moderation), ChangeCityInTx (teams). Транзакция — явным
// параметром; событие, письмо и строки identity пишутся в ней, аудит своего действия пишет вызывающий (кроме смены
// города: её автор — сам игрок, запись identity.city_changed — здесь). Импортировать пакет могут только пары
// archtest.IntxPairs; в плане I-4 вызывающих нет, кроме тестов (admin-api его не зовёт: у роли admin нет UPDATE на
// users — К2, R13).
//
// Правило условий капитанства (evaluateCaptaincy) здесь не считается: App собирается app.NewTx без портов условий
// (I7в ревью плана I-4); если когда-нибудь посчитается — без портов оно отказывает (checks_unavailable, К1).
//
// Обязанности вызывающего:
//   - tx — транзакция db.InTx из ctx или её SAVEPOINT (tx.Begin; так исполнитель подтверждений platform/approvals
//     получает tx): события (events.Publish) и аудит (audit.Write) берут tx из ctx, запросы — из параметра, и на одном
//     соединении они попадают в одну транзакцию (откат SAVEPOINT откатывает и их). Методы сверяют соединение
//     (Conn) и при чужом (tx другого db.InTx, ctx без транзакции) отказывают ErrTxMismatch до любых записей (m7, I2
//     ревью T7);
//   - порядок замков (R7): операции, берущие users (SetRestrictionsInTx, ChangeCityInTx), — в транзакции первыми,
//     RevokeRoleInTx (строка заявки, затем допуск; users не берёт) — после них. Обратный порядок против анонимизации
//     (users → заявки → допуски) даёт взаимную блокировку 40P01 (m6 ревью T7).
package intx

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/db"
)

// Deps — зависимости: пул роли вызывающего (api), часы, клиент River только для вставки (письмо смены города), geo,
// журнал. Все обязательны.
type Deps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Geo   geo.Service
	River *river.Client[pgx.Tx]
	Log   *slog.Logger
}

// Tx — операции identity в транзакции вызывающего.
type Tx struct{ a *app.App }

// New — сборка; без зависимости — ошибка (отказ старта, а не паника в запросе).
func New(d Deps) (*Tx, error) {
	a, err := app.NewTx(app.TxDeps{Pool: d.Pool, Clock: d.Clock, River: d.River, Geo: d.Geo, Log: d.Log})
	if err != nil {
		return nil, err
	}
	return &Tx{a: a}, nil
}

// Типы аргументов и итога.
type (
	// RevokeBy — кто отзывает допуск: сотрудник (StaffID, Label — подпись для аудита вызывающего) или система.
	RevokeBy = app.RevokeBy
	// Restriction — строка ограничения (Capability, Until; nil — бессрочно; login — бан).
	Restriction = app.Restriction
	// RestrictionsChange — изменение санкцией источника SourceID (LiftSource, Add, Status).
	RestrictionsChange = app.RestrictionsChange
	// RestrictionsResult — статус, статус до удаления, живые закрытые возможности, версия, отозвано сессий.
	RestrictionsResult = app.RestrictionsResult
)

// Статусы SetRestrictionsInTx.
const (
	StatusActive    = app.StatusActive
	StatusSuspended = app.StatusSuspended
	StatusBanned    = app.StatusBanned
)

// Ошибки: сравнивать errors.Is.
var (
	// ErrTxMismatch — tx из параметра на другом соединении, чем транзакция db.InTx в ctx (или ctx без транзакции): ничего
	// не записано.
	ErrTxMismatch     = errors.New("identity/intx: tx параметра не совпадает с транзакцией ctx")
	ErrUserNotFound   = identity.ErrUserNotFound
	ErrUserDeleted    = app.ErrUserDeleted
	ErrPermitNotFound = app.ErrPermitNotFound
	ErrCityNotFound   = app.ErrCityNotFound
	ErrBanNeedsLogin  = app.ErrBanNeedsLogin
	ErrLoginNeedsBan  = app.ErrLoginNeedsBan
)

// RevokeRoleInTx — отзыв живого допуска капитана игрока в городе (событие captain_permit_revoked на агрегате заявки
// допуска); нет допуска — ErrPermitNotFound; reason ≤ 500 символов. Аудит — у вызывающего.
func (t *Tx) RevokeRoleInTx(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID, by RevokeBy, reason string) error {
	if err := sameTx(ctx, tx); err != nil {
		return err
	}
	return t.a.RevokeRoleInTx(ctx, tx, userID, cityID, by, reason)
}

// sameTx — tx параметра на соединении транзакции db.InTx из ctx (там же события и аудит).
func sameTx(ctx context.Context, tx pgx.Tx) error {
	// соединение, а не объект: SAVEPOINT вызывающего — другой pgx.Tx на том же соединении той же транзакции
	if got, ok := db.TxFrom(ctx); !ok || got.Conn() != tx.Conn() {
		return ErrTxMismatch
	}
	return nil
}

// SetRestrictionsInTx — статус и ограничения аккаунта (R11, R12, R42): бан — только со строкой login (иначе
// ErrBanNeedsLogin), login — только с баном (ErrLoginNeedsBan); бан отзывает все сессии; в периоде удаления меняется
// status_before_deletion. Отказ не оставляет записей в tx вызывающего (SAVEPOINT). Аудит — у вызывающего.
func (t *Tx) SetRestrictionsInTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ch RestrictionsChange) (RestrictionsResult, error) {
	if err := sameTx(ctx, tx); err != nil {
		return RestrictionsResult{}, err
	}
	return t.a.SetRestrictionsInTx(ctx, tx, userID, ch)
}

// ChangeCityInTx — смена текущего города игрока (событие city_changed); открытая заявка прежнего города отзывается
// с письмом, без паузы (R39); нет города или страна выключена — ErrCityNotFound.
func (t *Tx) ChangeCityInTx(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID) error {
	if err := sameTx(ctx, tx); err != nil {
		return err
	}
	return t.a.ChangeCityInTx(ctx, tx, userID, cityID)
}
