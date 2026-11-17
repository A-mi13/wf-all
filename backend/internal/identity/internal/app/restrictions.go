package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
)

// Ограничения и статус аккаунта (план I-4, Task 7; спека §10.2, §17; R11, R12, R42) — SetRestrictionsInTx для
// identity/intx (вызывающий — moderation) и возврат статуса после истечения бана, общий с чисткой (banExpired).

// Ошибки межмодульных операций identity/intx (вызывающий решает, что ответить своему клиенту).
var (
	// ErrUserDeleted — аккаунт анонимизирован (deleted): менять нечего.
	ErrUserDeleted = errors.New("identity: аккаунт удалён")
	// ErrPermitNotFound — живого допуска капитана в городе нет (или отозван параллельно).
	ErrPermitNotFound = errors.New("identity: допуска капитана нет")
	// ErrCityNotFound — города нет или его страна выключена.
	ErrCityNotFound = errors.New("identity: город не найден")
	// ErrBanNeedsLogin — итоговый статус banned без живой строки ограничения login (бан — это строка login, §5.3).
	ErrBanNeedsLogin = errors.New("identity: бан без ограничения login")
	// ErrLoginNeedsBan — живая строка login при итоговом статусе не banned (R42: login ⇔ заблокирован).
	ErrLoginNeedsBan = errors.New("identity: ограничение login без бана")
)

// Статусы, которые задаёт SetRestrictionsInTx (pending_deletion и deleted — только сценарии удаления).
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusBanned    = "banned"
)

// Restriction — строка ограничения: Capability — закрытая возможность (login — бан), Until — до какого момента (nil —
// бессрочно; живёт строго до Until).
type Restriction struct {
	Capability string
	Until      *time.Time
}

// RestrictionsChange — изменение санкцией источника SourceID: LiftSource — снять живые строки источника (раньше Add),
// Add — новые строки источника, Status — "" (без изменения; снятие последней живой login у забаненного вернёт active,
// R12) | active | suspended | banned.
type RestrictionsChange struct {
	SourceID   uuid.UUID
	Status     string
	Add        []Restriction
	LiftSource bool
}

// RestrictionsResult — итог: Status — users.status (в периоде удаления — pending_deletion), StatusBeforeDeletion —
// статус, который вернёт отмена удаления ("" вне периода), Capabilities — живые закрытые возможности, Version —
// users.version после изменения, RevokedSessions — отозвано сессий баном.
type RestrictionsResult struct {
	Status               string
	StatusBeforeDeletion string
	Capabilities         []string
	Version              int64
	RevokedSessions      int
}

// effectiveStatus — статус аккаунта для правил бана: в периоде удаления — status_before_deletion (R11).
func effectiveStatus(status, before string) string {
	if status == string(identitydb.UserStatusPendingDeletion) {
		return before
	}
	return status
}

// banExpired — R12, одно правило для чистки и SetRestrictionsInTx: забаненный (в периоде удаления — по
// status_before_deletion) без живой строки login возвращается в active.
func banExpired(effective string, capabilities []string) bool {
	return effective == StatusBanned && !slices.Contains(capabilities, banCapability)
}

// expireBanTx — R12 одной функцией для чистки и отмены удаления (I1 ревью T7: бан истёк, чистка ещё не прошла):
// забаненный (в периоде удаления — status_before_deletion) без живой строки login (live — живые закрытые возможности)
// → active (SetUserStatus: в периоде удаления меняется status_before_deletion), версия + 1, событие
// restrictions_changed и аудит identity.ban_expired от system с реальным статусом. users под замком вызывающего;
// done = false — бан не истёк, ничего не записано.
func (a *App) expireBanTx(ctx context.Context, q *identitydb.Queries, userID uuid.UUID, status, before string,
	live []string) (done bool, err error) {
	if !banExpired(effectiveStatus(status, before), live) {
		return false, nil
	}
	row, err := q.SetUserStatus(ctx, identitydb.SetUserStatusParams{Status: identitydb.UserStatusActive, ID: userID})
	if err != nil {
		return false, fmt.Errorf("identity: статус после бана: %w", err)
	}
	after := map[string]any{"status": string(row.Status)}
	if row.Status == identitydb.UserStatusPendingDeletion {
		after["status_before_deletion"] = StatusActive
	}
	if err := audit.Write(ctx, audit.Entry{ActorRole: roleSystem, ActorLabel: roleSystem, Action: ActionBanExpired,
		ObjectType: objectUser, ObjectID: userID, After: after}); err != nil {
		return false, err
	}
	if _, err := events.Publish(ctx, identity.RestrictionsChanged(identity.UserRef{ID: userID, Version: row.Version},
		string(row.Status), live)); err != nil {
		return false, err
	}
	return true, nil
}

// SetRestrictionsInTx — статус и ограничения аккаунта в транзакции вызывающего (спека §10.2; R11, R12, R42): lockUser
// (users первым, R30); deleted → ErrUserDeleted. Изменение — под SAVEPOINT: отказ (ErrBanNeedsLogin,
// ErrLoginNeedsBan, сбой) не оставляет записей в tx вызывающего. Порядок: снятие строк источника, новые строки, итоговый
// статус (Status или прежний; прежний banned без живой login → active, R12), инвариант «живая login ⇔ banned» (R42),
// запись статуса — в периоде удаления только status_before_deletion, purge_at не трогается (R11); users.version + 1,
// событие restrictions_changed; бан — отзыв всех сессий (banned, событие sessions_revoked). Аудит пишет вызывающий
// (его действие).
func (a *App) SetRestrictionsInTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ch RestrictionsChange) (RestrictionsResult, error) {
	switch ch.Status {
	case "", StatusActive, StatusSuspended, StatusBanned:
	default:
		return RestrictionsResult{}, fmt.Errorf("identity: статус %q не задаётся ограничениями", ch.Status)
	}
	if ch.SourceID == uuid.Nil && (ch.LiftSource || len(ch.Add) > 0) {
		return RestrictionsResult{}, errors.New("identity: ограничения без источника")
	}
	u, err := a.lockUser(ctx, tx, userID)
	if errors.Is(err, errUserGone) {
		return RestrictionsResult{}, identity.ErrUserNotFound
	}
	if err != nil {
		return RestrictionsResult{}, err
	}
	if u.Status == identitydb.UserStatusDeleted {
		return RestrictionsResult{}, ErrUserDeleted
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return RestrictionsResult{}, fmt.Errorf("identity: точка сохранения ограничений: %w", err)
	}
	res, err := a.setRestrictions(ctx, sp, u, ch)
	if err != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return RestrictionsResult{}, errors.Join(err, rbErr)
		}
		return RestrictionsResult{}, err
	}
	if err := sp.Commit(ctx); err != nil {
		return RestrictionsResult{}, fmt.Errorf("identity: точка сохранения ограничений: %w", err)
	}
	return res, nil
}

func (a *App) setRestrictions(ctx context.Context, tx pgx.Tx, u userRow, ch RestrictionsChange) (RestrictionsResult, error) {
	q := identitydb.New(tx)
	now := a.clock.Now()
	var lifted int64
	if ch.LiftSource {
		var err error
		if lifted, err = q.RestrictionsLiftSource(ctx, identitydb.RestrictionsLiftSourceParams{Now: &now, UserID: u.ID,
			SourceID: ch.SourceID}); err != nil {
			return RestrictionsResult{}, fmt.Errorf("identity: снятие ограничений источника: %w", err)
		}
	}
	for _, r := range ch.Add {
		if err := q.RestrictionsAdd(ctx, identitydb.RestrictionsAddParams{ID: id.New(), UserID: u.ID,
			Capability: r.Capability, Until: r.Until, SourceID: ch.SourceID, Now: now}); err != nil {
			return RestrictionsResult{}, fmt.Errorf("identity: ограничение %s: %w", r.Capability, err)
		}
	}
	live, err := q.RestrictionsLiveCapabilities(ctx, identitydb.RestrictionsLiveCapabilitiesParams{UserID: u.ID, Now: now})
	if err != nil {
		return RestrictionsResult{}, fmt.Errorf("identity: действующие ограничения: %w", err)
	}
	before := ""
	if u.StatusBeforeDeletion != nil {
		before = string(*u.StatusBeforeDeletion)
	}
	prev := effectiveStatus(string(u.Status), before)
	target := ch.Status
	if target == "" {
		target = prev
		if banExpired(prev, live) {
			target = StatusActive
		}
	}
	login := slices.Contains(live, banCapability)
	switch {
	case target == StatusBanned && !login:
		return RestrictionsResult{}, ErrBanNeedsLogin
	case target != StatusBanned && login:
		return RestrictionsResult{}, ErrLoginNeedsBan
	}
	if lifted == 0 && len(ch.Add) == 0 && target == prev {
		// пустое изменение (m1 ревью T7): ни строк, ни статуса — без версии и события
		return RestrictionsResult{Status: string(u.Status), StatusBeforeDeletion: before, Capabilities: live,
			Version: u.Version}, nil
	}
	row, err := q.SetUserStatus(ctx, identitydb.SetUserStatusParams{Status: identitydb.UserStatus(target), ID: u.ID})
	if err != nil {
		return RestrictionsResult{}, fmt.Errorf("identity: статус аккаунта: %w", err)
	}
	if _, err := events.Publish(ctx, identity.RestrictionsChanged(identity.UserRef{ID: u.ID, Version: row.Version},
		string(row.Status), live)); err != nil {
		return RestrictionsResult{}, err
	}
	res := RestrictionsResult{Status: string(row.Status), Capabilities: live, Version: row.Version}
	if row.Status == identitydb.UserStatusPendingDeletion {
		res.StatusBeforeDeletion = target
	}
	if target == StatusBanned {
		ids, err := a.revokeSessions(ctx, tx, u.ID, uuid.Nil, domain.RevokedBanned)
		if err != nil {
			return RestrictionsResult{}, err
		}
		res.RevokedSessions = len(ids)
		if len(ids) > 0 {
			res.Version++ // sessionsRevoked подняла версию ещё раз
		}
	}
	return res, nil
}
