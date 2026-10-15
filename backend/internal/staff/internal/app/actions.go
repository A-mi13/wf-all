package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Действия подтверждения вдвоём модуля staff (спека §4.8; подписи — approvals.actions.<действие> в
// локалях админки).
const (
	ActionInvite      = "staff.invite"       // приглашение (P13): grant | grant_elevated по роли
	ActionRoleChange  = "staff.role_change"  // смена роли и городов (D16): grant | grant_elevated
	ActionAccessReset = "staff.access_reset" // сброс доступа: access.reset | access.reset_elevated по роли цели
	ActionRevokeOwner = "staff.revoke_owner" // выключение владельца: revoke_owner
)

// payloadVersion — версия формата payload действий staff (поле v).
const payloadVersion = 1

// payloadBase — общее у payload всех действий: версия формата и инициатор с версией его учётки на
// момент запроса (I1): проверка инициатора отказывает, если учётку с тех пор выключали, сбрасывали или
// меняли ей роль (version растёт), даже если она снова active с тем же правом.
type payloadBase struct {
	V       int       `json:"v"`
	InitID  uuid.UUID `json:"init_id"`
	InitVer int64     `json:"init_ver"`
}

func base(by acct) payloadBase {
	return payloadBase{V: payloadVersion, InitID: by.id, InitVer: by.version}
}

// invitePayload — staff.invite: кого пригласить; ExistingID — почта выключенной учётки (вернуть её
// сбросом доступа, §2.6), ExistingVersion — её версия на момент запроса.
type invitePayload struct {
	payloadBase
	Email           string      `json:"email"`
	DisplayName     string      `json:"display_name"`
	Role            string      `json:"role"`
	CityIDs         []uuid.UUID `json:"city_ids"`
	ExistingID      *uuid.UUID  `json:"existing_id,omitempty"`
	ExistingVersion int64       `json:"existing_version,omitempty"`
}

// rolePayload — staff.role_change: цель и её версия, было (право запроса по D16 считается из пары) и
// стало.
type rolePayload struct {
	payloadBase
	TargetID    uuid.UUID   `json:"target_id"`
	Version     int64       `json:"version"`
	FromRole    string      `json:"from_role"`
	FromCityIDs []uuid.UUID `json:"from_city_ids"`
	Role        string      `json:"role"`
	CityIDs     []uuid.UUID `json:"city_ids"`
}

// targetPayload — staff.access_reset и staff.revoke_owner: цель, её версия и роль на момент запроса,
// причина и комментарий (комментарий — в аудит исполнения как reason, не в before/after).
type targetPayload struct {
	payloadBase
	TargetID uuid.UUID `json:"target_id"`
	Version  int64     `json:"version"`
	Role     string    `json:"role"`
	Reason   string    `json:"reason"`
	Comment  string    `json:"comment,omitempty"`
}

// decode — payload действия формата payloadVersion. Иное — сбой (500, запрос pending): payload пишет
// только сам модуль.
func decode[T any](raw json.RawMessage, base func(*T) *payloadBase) (T, error) {
	var p T
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("staff: payload: %w", err)
	}
	if v := base(&p).V; v != payloadVersion {
		return p, fmt.Errorf("staff: payload v = %d, ждали %d", v, payloadVersion)
	}
	return p, nil
}

func (p *invitePayload) b() *payloadBase { return &p.payloadBase }
func (p *rolePayload) b() *payloadBase   { return &p.payloadBase }
func (p *targetPayload) b() *payloadBase { return &p.payloadBase }

// RegisterApprovals — действия staff.* и проверка инициатора в реестре платформы (сборка admin-api,
// 10c: approvals.New → RegisterApprovals → Registry.Validate). svc — сервис подтверждений для запросов и
// гашения (CancelByInitiator).
func (a *App) RegisterApprovals(r *approvals.Registry, svc *approvals.Service) {
	a.approvals = svc
	r.Register(approvals.Action{
		Name: ActionInvite, Permission: permGrant, Exec: a.execInvite,
		Permit: func(raw json.RawMessage) (string, error) {
			p, err := decode(raw, (*invitePayload).b)
			return invitePermission(p.Role), err
		},
		Target: func(raw json.RawMessage) (uuid.UUID, uuid.UUID) {
			p, _ := decode(raw, (*invitePayload).b)
			if p.ExistingID != nil {
				return *p.ExistingID, uuid.Nil
			}
			return uuid.Nil, uuid.Nil
		},
	})
	r.Register(approvals.Action{
		Name: ActionRoleChange, Permission: permGrant, Exec: a.execRoleChange,
		Permit: func(raw json.RawMessage) (string, error) {
			p, err := decode(raw, (*rolePayload).b)
			if err != nil {
				return "", err
			}
			permission, changed := rolePermission(p.FromRole, p.FromCityIDs, p.Role, p.CityIDs)
			if !changed {
				return "", errors.New("staff: смена роли без изменений")
			}
			return permission, nil
		},
		Target: func(raw json.RawMessage) (uuid.UUID, uuid.UUID) {
			p, _ := decode(raw, (*rolePayload).b)
			return p.TargetID, uuid.Nil
		},
	})
	r.Register(approvals.Action{
		Name: ActionAccessReset, Permission: permReset, Exec: a.execAccessReset,
		Permit: func(raw json.RawMessage) (string, error) {
			p, err := decode(raw, (*targetPayload).b)
			return resetPermission(p.Role), err
		},
		Target: targetOf,
	})
	r.Register(approvals.Action{
		Name: ActionRevokeOwner, Permission: permRevokeOwner, Exec: a.execRevokeOwner, Target: targetOf,
	})
	r.SetInitiatorCheck(a.checkInitiator)
}

func targetOf(raw json.RawMessage) (uuid.UUID, uuid.UUID) {
	p, _ := decode(raw, (*targetPayload).b)
	return p.TargetID, uuid.Nil
}

// initiatorValid — инициатор вправе, чтобы его запрос исполнился (D17, I1): учётка active и не закрыта,
// версия та же, что при запросе, и право запроса permission в городе cityID по ТЕКУЩЕЙ роли не Deny.
func initiatorValid(status, role string, version int64, lockedUntil *time.Time, p payloadBase, permission string, cityID uuid.UUID, now time.Time) bool {
	if status != statusActive || lockedUntil != nil && now.Before(*lockedUntil) || version != p.InitVer {
		return false
	}
	st := &auth.Staff{StaffID: p.InitID, Role: role, Stage: auth.StageActive, Grants: perm.Grants(perm.Role(role))}
	return st.Decide(permission, cityID) != auth.Deny
}

// checkInitiator — approvals.InitiatorCheck: предварительная проверка инициатора без замка (замок
// учётки инициатора здесь дал бы взаимную блокировку встречных запросов, m7: строка запроса → инициатор
// ∥ … → цель). Окончательно инициатора проверяет исполнитель под FOR SHARE в общем порядке замков по id.
// Отказ — Fail(ErrStale): запрос failed (initiator_changed). Payload в текст ошибки не попадает.
func (a *App) checkInitiator(ctx context.Context, tx pgx.Tx, ap approvals.Approval) error {
	var p payloadBase
	if err := json.Unmarshal(ap.Payload, &p); err != nil {
		return fmt.Errorf("staff: payload запроса %s не разбирается", ap.ID)
	}
	if p.InitID != ap.RequestedBy {
		return approvals.Fail(approvals.ErrStale)
	}
	st, err := staffdb.New(tx).ManageAccountState(ctx, ap.RequestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return approvals.Fail(approvals.ErrStale)
	}
	if err != nil {
		return err
	}
	if !initiatorValid(st.Status, st.Role, st.Version, st.LockedUntil, p, ap.Permission, ap.ScopeCityID, a.clock.Now()) {
		return approvals.Fail(approvals.ErrStale)
	}
	return nil
}

// execLocks — вход исполнителя: замок стража владельцев (owners), затем учётки по возрастанию id (цель
// — FOR UPDATE, инициатор и подтверждающий — FOR SHARE). Подтверждающий перепроверяется (m4: выключен,
// сменил роль, сессия погашена — обычная ошибка: откат всего, запрос остаётся pending), инициатор —
// под замком (изменился после запроса — ErrStale: failed). permission — право запроса (то же, что
// посчитал Permit).
func (a *App) execLocks(ctx context.Context, q *staffdb.Queries, p payloadBase, permission string, approver *auth.Staff, target uuid.UUID, owners bool) (map[uuid.UUID]acct, actor, error) {
	if owners {
		if err := lockOwners(ctx, q); err != nil {
			return nil, actor{}, err
		}
	}
	locked, err := lockAccounts(ctx, q, target, p.InitID, approver.StaffID)
	if err != nil {
		return nil, actor{}, err
	}
	now := a.clock.Now()
	by, err := checkActor(ctx, q, approver, locked, now)
	if err != nil {
		return nil, actor{}, err
	}
	init, ok := locked[p.InitID]
	if !ok || !initiatorValid(init.status, init.role, init.version, init.lockedUntil, p, permission, uuid.Nil, now) {
		return nil, actor{}, approvals.ErrStale
	}
	return locked, actorOf(approver, by), nil
}

// lockedTarget — цель исполнителя под замком и той же версии, что при запросе; иначе ErrStale.
func lockedTarget(locked map[uuid.UUID]acct, id uuid.UUID, version int64) (acct, error) {
	t, ok := locked[id]
	if !ok || t.version != version {
		return acct{}, approvals.ErrStale
	}
	return t, nil
}

// business — бизнес-отказ исполнителя как Fail (запрос failed, изменения откатываются savepoint):
// последний владелец, почта занята, города нет. Прочее — как есть.
func business(err error) error {
	switch {
	case isCityFK(err):
		return approvals.Fail(staff.ErrCityNotFound)
	case errors.Is(err, staff.ErrLastOwner), errors.Is(err, staff.ErrEmailTaken), errors.Is(err, staff.ErrCityNotFound):
		return approvals.Fail(err)
	}
	return err
}

// execInvite — исполнитель staff.invite (P13, §4.3): новая учётка invited с ролью и городами (created_by
// и granted_by — подтвердивший), приглашение invite (письмо), аудит и событие staff.invited; почта занята
// (гонка двух приглашений) — Fail(staff.email_taken), ничего не создано. Почта выключенной учётки
// (existing_id, версия та же) — та же учётка снова: новое имя, роль и города, сброс доступа, приглашение
// invite.
func (a *App) execInvite(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	p, err := decode(raw, (*invitePayload).b)
	if err != nil {
		return err
	}
	q := staffdb.New(tx)
	target := uuid.Nil
	if p.ExistingID != nil {
		target = *p.ExistingID
	}
	locked, by, err := a.execLocks(ctx, q, p.payloadBase, invitePermission(p.Role), approver, target, false)
	if err != nil {
		return err
	}
	cities := p.CityIDs
	if cities == nil {
		cities = []uuid.UUID{}
	}
	after := map[string]any{"role": p.Role, "city_ids": cities, "email": p.Email, "requested_by": p.InitID}
	if p.ExistingID != nil {
		t, err := lockedTarget(locked, target, p.ExistingVersion)
		if err != nil {
			return err
		}
		if t.status != statusDisabled {
			return approvals.ErrStale
		}
		return business(a.reinvite(ctx, tx, q, t, p, by, after))
	}
	now := a.clock.Now()
	c, err := a.createInvited(ctx, q, newAccount{email: p.Email, name: p.DisplayName, role: p.Role, cities: cities, by: by.staffID}, now)
	if err != nil {
		return business(err)
	}
	if _, err := a.IssueInvite(ctx, tx, c.id, purposeInvite); err != nil {
		return err
	}
	if err := audit.Write(ctx, by.entry(actionInvited, c.id, nil, after, staff.Meta{})); err != nil {
		return err
	}
	return publishInvited(ctx, c, p.Role)
}

// reinvite — почта выключенной учётки t приглашена снова (§2.6): роль и города из запроса, затем сброс
// доступа с новым именем и приглашением invite; аудит и событие staff.invited (reinvited).
func (a *App) reinvite(ctx context.Context, tx pgx.Tx, q *staffdb.Queries, t acct, p invitePayload, by actor, after map[string]any) error {
	now := a.clock.Now()
	if err := q.ManageSetRole(ctx, staffdb.ManageSetRoleParams{Role: p.Role, GrantedBy: optID(by.staffID), Now: now, StaffID: t.id}); err != nil {
		return err
	}
	if err := q.ManageDeleteCities(ctx, t.id); err != nil {
		return err
	}
	if len(p.CityIDs) > 0 {
		if err := q.InsertRoleCities(ctx, staffdb.InsertRoleCitiesParams{StaffID: t.id, CityIds: p.CityIDs}); err != nil {
			return err
		}
	}
	after["reinvited"] = true
	name := p.DisplayName
	_, err := a.resetAccess(ctx, tx, q, t, resetSpec{
		by: by, action: actionInvited, after: after, purpose: purposeInvite, name: &name,
		event: func(v int64) events.Event { return staff.InvitedEvent(t.id, v, p.Role) },
	})
	return err
}

// execRoleChange — исполнитель staff.role_change (D16): цель той же версии, что при запросе (иначе
// ErrStale), страж последнего владельца при понижении, applyRole от подтвердившего.
func (a *App) execRoleChange(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	p, err := decode(raw, (*rolePayload).b)
	if err != nil {
		return err
	}
	permission, changed := rolePermission(p.FromRole, p.FromCityIDs, p.Role, p.CityIDs)
	if !changed {
		return approvals.ErrStale
	}
	q := staffdb.New(tx)
	locked, by, err := a.execLocks(ctx, q, p.payloadBase, permission, approver, p.TargetID, true)
	if err != nil {
		return err
	}
	t, err := lockedTarget(locked, p.TargetID, p.Version)
	if err != nil {
		return err
	}
	from, err := citiesOf(ctx, q, t.id)
	if err != nil {
		return err
	}
	cities := p.CityIDs
	if cities == nil {
		cities = []uuid.UUID{}
	}
	return business(a.applyRole(ctx, tx, q, t, from, p.Role, cities, by, p.InitID, staff.Meta{}, a.clock.Now()))
}

// execAccessReset — исполнитель staff.access_reset (§4.9): цель той же версии, страж последнего
// владельца, сброс доступа с приглашением access_reset; аудит и событие staff.access_reset.
func (a *App) execAccessReset(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	p, err := decode(raw, (*targetPayload).b)
	if err != nil {
		return err
	}
	q := staffdb.New(tx)
	locked, by, err := a.execLocks(ctx, q, p.payloadBase, resetPermission(p.Role), approver, p.TargetID, true)
	if err != nil {
		return err
	}
	t, err := lockedTarget(locked, p.TargetID, p.Version)
	if err != nil {
		return err
	}
	if err := ownerGuard(ctx, q, t); err != nil {
		return business(err)
	}
	_, err = a.resetAccess(ctx, tx, q, t, resetSpec{
		by: by, action: actionAccessReset, reason: reasonText(p.Reason, p.Comment),
		after: map[string]any{"status": statusInvited, "requested_by": p.InitID}, purpose: purposeAccessReset,
		event: func(v int64) events.Event { return staff.AccessResetEvent(t.id, v) },
	})
	return err
}

// execRevokeOwner — исполнитель staff.revoke_owner (§2.6): цель той же версии, выключение под стражем
// последнего владельца.
func (a *App) execRevokeOwner(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	p, err := decode(raw, (*targetPayload).b)
	if err != nil {
		return err
	}
	q := staffdb.New(tx)
	locked, by, err := a.execLocks(ctx, q, p.payloadBase, permRevokeOwner, approver, p.TargetID, true)
	if err != nil {
		return err
	}
	t, err := lockedTarget(locked, p.TargetID, p.Version)
	if err != nil {
		return err
	}
	if t.status == statusDisabled {
		return approvals.ErrStale
	}
	return business(a.disable(ctx, tx, q, t, by, p.InitID, reasonText(p.Reason, p.Comment), staff.Meta{}))
}
