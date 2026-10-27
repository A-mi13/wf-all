package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/domain"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Управление сотрудниками (спека §2.5–2.6, §4.9; D16, D17, P13): приглашение, смена роли и городов,
// выключение, сброс доступа. Сценарий решает по праву (Decide): Allow — исполняет сразу, NeedsApproval —
// создаёт запрос подтверждения вдвоём (исполнители — actions.go), Deny — 403.
//
// Порядок замков (m7, M-4): pg_advisory_xact_lock(ownersLockKey) — только там, где возможен страж
// последнего владельца, всегда первым → учётки по возрастанию id (цель — FOR UPDATE, автор, инициатор,
// подтверждающий — FOR SHARE) → сессии → staff_mfa → резервные коды → приглашения. Путь Approve перед
// этим держит строку approvals (платформа). Встречные запросы и параллельные пути взаимно не ждут по
// кругу.

// Права управления (perm, §2.5).
const (
	permGrant          = "staff.roles.grant"
	permGrantElevated  = "staff.roles.grant_elevated"
	permRevoke         = "staff.roles.revoke"
	permRevokeAdmin    = "staff.roles.revoke_admin"
	permRevokeOwner    = "staff.roles.revoke_owner"
	permReset          = "staff.access.reset"
	permResetElevated  = "staff.access.reset_elevated"
	permAccountsRead   = "staff.accounts.read"
	permSessionsRevoke = "staff.sessions.revoke"
)

// ownersLockKey — ключ pg_advisory_xact_lock стража последнего владельца (D17): один на приложение,
// «staff_ow» в ASCII. Другие advisory-замки этим числом не пользуются.
const ownersLockKey int64 = 0x73746166665f6f77

// Поля и правила 400 validation.failed (контракт: StaffInviteRequest, StaffRoleUpdate,
// StaffConfirmedAction).
const (
	fieldRole     = "body.role"
	fieldCityIDs  = "body.city_ids"
	fieldReason   = "body.reason"
	fieldComment  = "body.comment"
	fieldConfirm  = "body.confirm"
	ruleMinItems  = "min_items"
	ruleMaxItems  = "max_items"
	ruleMismatch  = "mismatch"
	maxCities     = 100 // StaffInviteRequest.city_ids, StaffRoleUpdate.city_ids: maxItems
	maxComment    = 500 // StaffConfirmedAction.comment: maxLength
	reasonByOper  = "operator"
	roleModerator = auth.RoleModerator
)

// errNoApprovals — сценарий «вдвоём» до RegisterApprovals (ошибка сборки admin-api).
var errNoApprovals = errors.New("staff: подтверждения не зарегистрированы (RegisterApprovals)")

// acct — учётка под замком управления.
type acct struct {
	id                        uuid.UUID
	status, role, name, email string
	version                   int64
	lockedUntil               *time.Time
}

// lockAccounts — замки учёток в порядке возрастания id: target — FOR UPDATE (uuid.Nil — нет цели),
// shared — FOR SHARE (uuid.Nil и повтор пропускаются). Учётки, которой нет, в карте нет.
func lockAccounts(ctx context.Context, q *staffdb.Queries, target uuid.UUID, shared ...uuid.UUID) (map[uuid.UUID]acct, error) {
	ids := make([]uuid.UUID, 0, len(shared)+1)
	for _, u := range append(slices.Clone(shared), target) {
		if u != uuid.Nil && !slices.Contains(ids, u) {
			ids = append(ids, u)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	out := make(map[uuid.UUID]acct, len(ids))
	for _, u := range ids {
		var (
			a   acct
			err error
		)
		if u == target {
			var r staffdb.ManageLockAccountRow
			r, err = q.ManageLockAccount(ctx, u)
			a = acct{id: r.ID, status: r.Status, role: r.Role, name: r.DisplayName, email: r.Email, version: r.Version, lockedUntil: r.LockedUntil}
		} else {
			var r staffdb.ManageShareAccountRow
			r, err = q.ManageShareAccount(ctx, u)
			a = acct{id: r.ID, status: r.Status, role: r.Role, name: r.DisplayName, email: r.Email, version: r.Version, lockedUntil: r.LockedUntil}
		}
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			continue
		case err != nil:
			return nil, err
		}
		out[u] = a
	}
	return out, nil
}

// checkActor — сотрудник s (автор действия или подтверждающий) в момент действия, его учётка уже под
// замком (M-4, m4): учётки нет, она не active или закрыта, сессия погашена, истекла или не active —
// auth.ErrSessionInvalid; сессию заменили новой (вход, unlock, принятие в другой вкладке) —
// auth.ErrSessionReplaced: 401 без стирания свежей cookie (уборка №8 m1; окно replacedGrace не проверяется —
// см. replacedAsSession); роль сменилась после загрузки сессии (права снимка устарели) — 403. Гашение
// сессий и смена роли идут под замком учётки, поэтому под FOR SHARE ответ не устареет до коммита.
func checkActor(ctx context.Context, q *staffdb.Queries, s *auth.Staff, locked map[uuid.UUID]acct, now time.Time) (acct, error) {
	a, ok := locked[s.StaffID]
	if !ok || a.status != statusActive || a.lockedUntil != nil && now.Before(*a.lockedUntil) {
		return a, auth.ErrSessionInvalid
	}
	if a.role != s.Role {
		return a, errForbidden
	}
	sess, err := q.ManageSessionState(ctx, s.SessionID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return a, auth.ErrSessionInvalid
	case err != nil:
		return a, err
	case sess.StaffID != s.StaffID:
		return a, auth.ErrSessionInvalid
	case sess.RevokedAt != nil && sess.RevokedReason != nil && *sess.RevokedReason == reasonReplaced:
		return a, auth.ErrSessionReplaced
	case sess.RevokedAt != nil || !now.Before(sess.ExpiresAt) || sess.Stage != auth.StageActive:
		return a, auth.ErrSessionInvalid
	}
	return a, nil
}

// actorOf — автор аудита: сотрудник s со снимком имени из учётки под замком.
func actorOf(s *auth.Staff, a acct) actor {
	return actor{staffID: s.StaffID, role: s.Role, label: label(a.name)}
}

// lockOwners — замок стража последнего владельца: первым в транзакции, до замков учёток.
func lockOwners(ctx context.Context, q *staffdb.Queries) error {
	return q.ManageOwnersLock(ctx, ownersLockKey)
}

// ownerGuard — страж последнего владельца (D17, §2.6): t — активный владелец, а других активных
// владельцев нет — staff.ErrLastOwner. Только под lockOwners той же транзакции: счёт — после замка,
// свежим снимком.
func ownerGuard(ctx context.Context, q *staffdb.Queries, t acct) error {
	if t.status != statusActive || t.role != roleOwner {
		return nil
	}
	n, err := q.ManageCountOtherActiveOwners(ctx, t.id)
	if err != nil {
		return err
	}
	if n == 0 {
		return staff.ErrLastOwner
	}
	return nil
}

// validRole — роль из perm.Roles, иначе 400 по полю field (enum).
func validRole(role, field string) error {
	if !slices.Contains(perm.Roles, perm.Role(role)) {
		return httpx.NewFieldError(field, ruleEnum)
	}
	return nil
}

// normCities — города роли: у модератора 1–100 (body.city_ids min_items | max_items), у других ролей —
// ни одного (max_items); без повторов, по возрастанию (сравнение «было/стало» — как множеств).
func normCities(role string, cities []uuid.UUID) ([]uuid.UUID, error) {
	switch {
	case role == roleModerator && len(cities) == 0:
		return nil, httpx.NewFieldError(fieldCityIDs, ruleMinItems)
	case role != roleModerator && len(cities) > 0, len(cities) > maxCities:
		return nil, httpx.NewFieldError(fieldCityIDs, ruleMaxItems)
	}
	out := make([]uuid.UUID, 0, len(cities))
	for _, c := range cities {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out, nil
}

// citiesExist — все города существуют (справочник geo — через geo_read_city_settings), иначе 422
// staff.city_not_found.
func citiesExist(ctx context.Context, q *staffdb.Queries, cities []uuid.UUID) error {
	if len(cities) == 0 {
		return nil
	}
	n, err := q.ManageKnownCities(ctx, cities)
	if err != nil {
		return err
	}
	if int(n) != len(cities) {
		return staff.ErrCityNotFound
	}
	return nil
}

// citiesOf — города сотрудника по возрастанию (у не-модератора — пустой список).
func citiesOf(ctx context.Context, q *staffdb.Queries, staffID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.ManageCitiesOf(ctx, []uuid.UUID{staffID})
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.CityID)
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out, nil
}

// isCityFK — нарушение внешнего ключа города модератора (город исчез между проверкой и записью).
func isCityFK(err error) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code == "23503" && pe.TableName == "staff_role_cities"
}

// checkReason — причина из списка (body.reason enum) и комментарий без крайних пробелов, не длиннее 500
// символов (body.comment maxLength).
func checkReason(reason, comment string) (string, error) {
	if !slices.Contains(staff.Reasons, reason) {
		return "", httpx.NewFieldError(fieldReason, ruleEnum)
	}
	comment = strings.TrimSpace(comment)
	if utf8.RuneCountInString(comment) > maxComment {
		return "", httpx.NewFieldError(fieldComment, ruleMaxLength)
	}
	return comment, nil
}

// reasonText — причина в аудите (Record.Reason): «код» или «код: комментарий». Письмо U1 берёт только
// код — часть до первого ": ".
func reasonText(reason, comment string) string {
	if comment == "" {
		return reason
	}
	return reason + ": " + comment
}

// confirmMatches — почта, введённая руками (§4.9), после нормализации (регистр, пробелы, IDN → xn--)
// совпадает с почтой цели.
func confirmMatches(confirm, email string) bool {
	norm, ok := domain.NormalizeEmail(confirm)
	return ok && norm == email
}

// elevated — роль владельца или админа.
func elevated(role string) bool { return role == roleOwner || role == roleAdmin }

// invitePermission — право приглашения с ролью role (P13).
func invitePermission(role string) string {
	if elevated(role) {
		return permGrantElevated
	}
	return permGrant
}

// rolePermission — право смены роли и городов по таблице D16: старая или новая роль owner/admin —
// grant_elevated; роль меняется или добавлен город — grant; иначе (только снятие городов) — revoke.
// changed=false — изменений нет. Города — по возрастанию, без повторов.
func rolePermission(from string, fromCities []uuid.UUID, to string, toCities []uuid.UUID) (string, bool) {
	if from == to && slices.Equal(fromCities, toCities) {
		return "", false
	}
	if elevated(from) || elevated(to) {
		return permGrantElevated, true
	}
	if from != to {
		return permGrant, true
	}
	for _, c := range toCities {
		if !slices.Contains(fromCities, c) {
			return permGrant, true
		}
	}
	return permRevoke, true
}

// disablePermission — право выключения учётки с ролью role (§2.6).
func disablePermission(role string) string {
	switch role {
	case roleOwner:
		return permRevokeOwner
	case roleAdmin:
		return permRevokeAdmin
	}
	return permRevoke
}

// resetPermission — право сброса доступа учётке с ролью role (§4.9).
func resetPermission(role string) string {
	if elevated(role) {
		return permResetElevated
	}
	return permReset
}

// request — запрос подтверждения вдвоём в транзакции сценария: снимок имени и метаданные инициатора.
func (a *App) request(ctx context.Context, tx pgx.Tx, s *auth.Staff, by acct, m staff.Meta, action string, payload any) (approvals.Approval, error) {
	if a.approvals == nil {
		return approvals.Approval{}, errNoApprovals
	}
	return a.approvals.Request(ctx, tx, s, label(by.name), approvals.Meta{IP: m.IP, UserAgent: m.UserAgent}, action, payload)
}

// cancelRequests — ожидающие запросы сотрудника staffID гасятся (D17: выключение, сброс доступа, смена
// роли). Строку под замком параллельного решения гашение пропускает — её отсечёт версия инициатора.
func (a *App) cancelRequests(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) error {
	if a.approvals == nil {
		return errNoApprovals
	}
	_, err := a.approvals.CancelByInitiator(ctx, tx, staffID)
	return err
}

// Invite — приглашение сотрудника (P13, §4.3): всегда запрос подтверждения (202). Почта и имя — как у
// bootstrap (validateInvitee), роль — из списка, города — только у модератора (1–100, существуют —
// иначе 422). Право — grant_elevated для owner/admin, иначе grant (Deny — 403 до обращения к базе).
// Почта действующей или приглашённой учётки — staff.email_taken; выключенной — запрос с existing_id и её
// версией (исполнение вернёт ту же учётку сбросом доступа, §2.6).
func (a *App) Invite(ctx context.Context, s *auth.Staff, in staff.InviteInput, m staff.Meta) (approvals.Approval, error) {
	email, name, err := validateInvitee(in.Email, in.DisplayName)
	if err != nil {
		return approvals.Approval{}, err
	}
	if err := validRole(in.Role, fieldRole); err != nil {
		return approvals.Approval{}, err
	}
	cities, err := normCities(in.Role, in.CityIDs)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.Decide(invitePermission(in.Role), uuid.Nil) == auth.Deny {
		return approvals.Approval{}, errForbidden
	}
	var out approvals.Approval
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		locked, err := lockAccounts(ctx, q, uuid.Nil, s.StaffID)
		if err != nil {
			return err
		}
		by, err := checkActor(ctx, q, s, locked, a.clock.Now())
		if err != nil {
			return err
		}
		p := invitePayload{payloadBase: base(by), Email: email, DisplayName: name, Role: in.Role, CityIDs: cities}
		ex, err := q.ManageFindByEmail(ctx, email)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case ex.Status != statusDisabled:
			return staff.ErrEmailTaken
		default:
			p.ExistingID, p.ExistingVersion = &ex.ID, ex.Version
		}
		if err := citiesExist(ctx, q, cities); err != nil {
			return err
		}
		out, err = a.request(ctx, tx, s, by, m, ActionInvite, p)
		return err
	})
	if err != nil {
		return approvals.Approval{}, err
	}
	return out, nil
}

// ChangeRole — новая роль и города целиком (D16, §2.6). Без изменений — nil, nil (без записей); право
// по таблице D16: Allow (только снятие городов) — исполняется сразу (nil, nil), NeedsApproval — запрос
// (202), Deny — 403. Над собой — staff.self_action; нет учётки — staff.not_found; выключенная — 403
// (вернуть её — только приглашением, право grant). Понижение активного владельца — под стражем
// последнего владельца (и при запросе, и при исполнении).
func (a *App) ChangeRole(ctx context.Context, s *auth.Staff, staffID uuid.UUID, role perm.Role, cities []uuid.UUID, m staff.Meta) (*approvals.Approval, error) {
	to := string(role)
	if err := validRole(to, fieldRole); err != nil {
		return nil, err
	}
	cities, err := normCities(to, cities)
	if err != nil {
		return nil, err
	}
	if staffID == s.StaffID {
		return nil, staff.ErrSelfAction
	}
	var out *approvals.Approval
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		locked, err := lockAccounts(ctx, q, staffID, s.StaffID)
		if err != nil {
			return err
		}
		now := a.clock.Now()
		by, err := checkActor(ctx, q, s, locked, now)
		if err != nil {
			return err
		}
		t, ok := locked[staffID]
		if !ok {
			return staff.ErrNotFound
		}
		if t.status == statusDisabled {
			return errForbidden
		}
		from, err := citiesOf(ctx, q, staffID)
		if err != nil {
			return err
		}
		permission, changed := rolePermission(t.role, from, to, cities)
		if !changed {
			return nil
		}
		decision := s.Decide(permission, uuid.Nil)
		if decision == auth.Deny {
			return errForbidden
		}
		if err := citiesExist(ctx, q, cities); err != nil {
			return err
		}
		if decision == auth.Allow {
			return a.applyRole(ctx, tx, q, t, from, to, cities, actorOf(s, by), uuid.Nil, m, now)
		}
		if to != roleOwner {
			if err := ownerGuard(ctx, q, t); err != nil { // ранний отказ: понижение последнего владельца
				return err
			}
		}
		ap, err := a.request(ctx, tx, s, by, m, ActionRoleChange, rolePayload{
			payloadBase: base(by), TargetID: t.id, Version: t.version,
			FromRole: t.role, FromCityIDs: from, Role: to, CityIDs: cities,
		})
		out = &ap
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// applyRole — смена роли и городов учётки t под её замком (сразу или исполнителем): страж последнего
// владельца при понижении, роль (granted_by — by), города целиком, версия +1, ожидающие запросы
// сотрудника гасятся, аудит staff.role_changed (было/стало; requestedBy — инициатор запроса, uuid.Nil —
// действие сразу), событие staff.role_granted (роль и города целиком) или при одном снятии городов —
// staff.role_revoked с removed_city_ids.
func (a *App) applyRole(ctx context.Context, tx pgx.Tx, q *staffdb.Queries, t acct, from []uuid.UUID, to string, cities []uuid.UUID, by actor, requestedBy uuid.UUID, m staff.Meta, now time.Time) error {
	if to != roleOwner {
		if err := ownerGuard(ctx, q, t); err != nil {
			return err
		}
	}
	if to != t.role {
		if err := q.ManageSetRole(ctx, staffdb.ManageSetRoleParams{Role: to, GrantedBy: optID(by.staffID), Now: now, StaffID: t.id}); err != nil {
			return err
		}
	}
	if !slices.Equal(from, cities) {
		if err := q.ManageDeleteCities(ctx, t.id); err != nil {
			return err
		}
		if len(cities) > 0 {
			if err := q.InsertRoleCities(ctx, staffdb.InsertRoleCitiesParams{StaffID: t.id, CityIds: cities}); err != nil {
				return err
			}
		}
	}
	ver, err := q.ManageBumpVersion(ctx, t.id)
	if err != nil {
		return err
	}
	if err := a.cancelRequests(ctx, tx, t.id); err != nil {
		return err
	}
	after := map[string]any{"role": to, "city_ids": cities}
	if requestedBy != uuid.Nil {
		after["requested_by"] = requestedBy
	}
	if err := audit.Write(ctx, by.entry(actionRoleChanged, t.id, map[string]any{"role": t.role, "city_ids": from}, after, m)); err != nil {
		return err
	}
	ev := staff.RoleGrantedEvent(t.id, ver, to, nilIfEmpty(cities))
	if removed := citiesRemoved(from, cities); to == t.role && removed != nil && !citiesAdded(from, cities) {
		ev = staff.CitiesRevokedEvent(t.id, ver, removed)
	}
	_, err = events.Publish(ctx, ev)
	return err
}

// citiesRemoved — города из from, которых нет в to (nil — ни одного).
func citiesRemoved(from, to []uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, c := range from {
		if !slices.Contains(to, c) {
			out = append(out, c)
		}
	}
	return out
}

// citiesAdded — в to есть город, которого нет в from.
func citiesAdded(from, to []uuid.UUID) bool { return len(citiesRemoved(to, from)) > 0 }

func nilIfEmpty(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// optID — uuid.Nil как NULL (оператор).
func optID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}

// Disable — выключить учётку (снять роль, §2.6, §4.9): reason из списка, comment ≤ 500, confirm — почта
// цели. Над собой — staff.self_action; нет учётки — staff.not_found; право по роли цели: модератор и
// поддержка — revoke, админ — revoke_admin (сразу; остальным владельцам — письмо U1), владелец —
// revoke_owner (вдвоём, запрос staff.revoke_owner); Deny — 403; уже выключена — staff.wrong_stage;
// почта не совпала — 400 body.confirm mismatch. Сразу — nil, nil.
func (a *App) Disable(ctx context.Context, s *auth.Staff, staffID uuid.UUID, reason, comment, confirm string, m staff.Meta) (*approvals.Approval, error) {
	comment, err := checkReason(reason, comment)
	if err != nil {
		return nil, err
	}
	if staffID == s.StaffID {
		return nil, staff.ErrSelfAction
	}
	var out *approvals.Approval
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		t, by, _, err := a.lockTarget(ctx, q, s, staffID)
		if err != nil {
			return err
		}
		decision := s.Decide(disablePermission(t.role), uuid.Nil)
		switch {
		case decision == auth.Deny:
			return errForbidden
		case t.status == statusDisabled:
			return staff.ErrWrongStage
		case !confirmMatches(confirm, t.email):
			return httpx.NewFieldError(fieldConfirm, ruleMismatch)
		case decision == auth.Allow:
			return a.disable(ctx, tx, q, t, actorOf(s, by), s.StaffID, reasonText(reason, comment), m)
		case t.role != roleOwner:
			return errForbidden // вдвоём выключается только владелец (staff.revoke_owner)
		}
		if err := ownerGuard(ctx, q, t); err != nil { // ранний отказ
			return err
		}
		ap, err := a.request(ctx, tx, s, by, m, ActionRevokeOwner, targetPayload{
			payloadBase: base(by), TargetID: t.id, Version: t.version, Role: t.role, Reason: reason, Comment: comment,
		})
		out = &ap
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// lockTarget — общий вход выключения и сброса доступа: замок стража владельцев, затем учётки по id
// (цель — FOR UPDATE, автор — FOR SHARE), автор перепроверен; цели нет — staff.not_found.
func (a *App) lockTarget(ctx context.Context, q *staffdb.Queries, s *auth.Staff, staffID uuid.UUID) (acct, acct, time.Time, error) {
	if err := lockOwners(ctx, q); err != nil {
		return acct{}, acct{}, time.Time{}, err
	}
	locked, err := lockAccounts(ctx, q, staffID, s.StaffID)
	if err != nil {
		return acct{}, acct{}, time.Time{}, err
	}
	now := a.clock.Now()
	by, err := checkActor(ctx, q, s, locked, now)
	if err != nil {
		return acct{}, acct{}, now, err
	}
	t, ok := locked[staffID]
	if !ok {
		return acct{}, by, now, staff.ErrNotFound
	}
	return t, by, now, nil
}

// disable — выключение учётки t под её замком (сразу, исполнителем staff.revoke_owner, оператором):
// страж последнего владельца, status = disabled и версия +1, все сессии гасятся (disabled), живое
// приглашение гасится, ожидающие запросы сотрудника гасятся, аудит staff.disabled (причина — reason),
// событие staff.role_revoked. Выключен админ — письмо staff.admin_disabled каждому активному владельцу,
// кроме инициатора (U1; initiator — uuid.Nil у оператора): ref — id записи аудита.
func (a *App) disable(ctx context.Context, tx pgx.Tx, q *staffdb.Queries, t acct, by actor, initiator uuid.UUID, reason string, m staff.Meta) error {
	if err := ownerGuard(ctx, q, t); err != nil {
		return err
	}
	now := a.clock.Now()
	ver, err := q.ManageDisable(ctx, t.id)
	if err != nil {
		return err
	}
	if _, err := revokeAccountSessions(ctx, q, t.id, reasonDisabled, now); err != nil {
		return err
	}
	if _, err := q.RevokeLiveInvite(ctx, staffdb.RevokeLiveInviteParams{Now: now, StaffID: t.id}); err != nil {
		return err
	}
	if err := a.cancelRequests(ctx, tx, t.id); err != nil {
		return err
	}
	auditID := id.New()
	e := by.entry(actionDisabled, t.id, map[string]any{"status": t.status},
		map[string]any{"status": statusDisabled, "role": t.role}, m)
	e.ID, e.Reason = auditID, reason
	if err := audit.Write(ctx, e); err != nil {
		return err
	}
	if _, err := events.Publish(ctx, staff.RoleRevokedEvent(t.id, ver, t.role)); err != nil {
		return err
	}
	if t.role != roleAdmin {
		return nil
	}
	owners, err := q.ListActiveOwners(ctx)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o == initiator {
			continue
		}
		if _, err := a.river.InsertTx(ctx, tx, staff.NewMailArgs(staff.MailAdminDisabled, auditID, o), nil); err != nil {
			return fmt.Errorf("staff: письмо staff.admin_disabled: %w", err)
		}
	}
	return nil
}

// ResetAccess — сброс доступа (§4.9): всегда запрос (202). reason, comment, confirm — как у Disable;
// право по роли цели: owner/admin — access.reset_elevated, иначе access.reset (Deny — 403); над собой —
// staff.self_action; выключенная — 403 (вернуть её — приглашением); активный последний владелец —
// staff.last_owner (ранний отказ).
func (a *App) ResetAccess(ctx context.Context, s *auth.Staff, staffID uuid.UUID, reason, comment, confirm string, m staff.Meta) (approvals.Approval, error) {
	comment, err := checkReason(reason, comment)
	if err != nil {
		return approvals.Approval{}, err
	}
	if staffID == s.StaffID {
		return approvals.Approval{}, staff.ErrSelfAction
	}
	var out approvals.Approval
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		t, by, _, err := a.lockTarget(ctx, q, s, staffID)
		if err != nil {
			return err
		}
		switch {
		case s.Decide(resetPermission(t.role), uuid.Nil) == auth.Deny, t.status == statusDisabled:
			return errForbidden
		case !confirmMatches(confirm, t.email):
			return httpx.NewFieldError(fieldConfirm, ruleMismatch)
		}
		if err := ownerGuard(ctx, q, t); err != nil {
			return err
		}
		out, err = a.request(ctx, tx, s, by, m, ActionAccessReset, targetPayload{
			payloadBase: base(by), TargetID: t.id, Version: t.version, Role: t.role, Reason: reason, Comment: comment,
		})
		return err
	})
	if err != nil {
		return approvals.Approval{}, err
	}
	return out, nil
}

// resetAccess — сброс доступа учётки t под её замком (исполнитель staff.access_reset, оператор):
// пароль и блокировка второго фактора стёрты, status = invited, версия +1 (name — новое имя или пусто);
// сессии гасятся (access_reset), staff_mfa и резервные коды удаляются, ожидающие запросы сотрудника
// гасятся, новое приглашение purpose (письмо — задачей в той же транзакции), аудит action (причина —
// reason) и событие. Код приглашения — вызывающему (оператор печатает с --print-code).
func (a *App) resetAccess(ctx context.Context, tx pgx.Tx, q *staffdb.Queries, t acct, r resetSpec) (string, error) {
	now := a.clock.Now()
	ver, err := q.ManageResetAccess(ctx, staffdb.ManageResetAccessParams{DisplayName: r.name, ID: t.id})
	if err != nil {
		return "", err
	}
	// порядок замков: учётка → сессии → staff_mfa → резервные коды → приглашения
	if _, err := revokeAccountSessions(ctx, q, t.id, reasonAccessReset, now); err != nil {
		return "", err
	}
	if err := q.DeleteMFA(ctx, t.id); err != nil {
		return "", err
	}
	if err := q.DeleteRecoveryCodes(ctx, t.id); err != nil {
		return "", err
	}
	if err := a.cancelRequests(ctx, tx, t.id); err != nil {
		return "", err
	}
	code, err := a.IssueInvite(ctx, tx, t.id, r.purpose)
	if err != nil {
		return "", err
	}
	e := r.by.entry(r.action, t.id, map[string]any{"status": t.status, "role": t.role}, r.after, r.m)
	e.Reason = r.reason
	if err := audit.Write(ctx, e); err != nil {
		return "", err
	}
	if _, err := events.Publish(ctx, r.event(ver)); err != nil {
		return "", err
	}
	return code, nil
}

// resetSpec — что делает resetAccess: автор, аудит, событие и приглашение.
type resetSpec struct {
	by      actor
	action  string // staff.access_reset | staff.invited (выключенная — снова)
	reason  string
	after   map[string]any
	purpose string
	name    *string // новое имя (повторное приглашение); nil — прежнее
	event   func(version int64) events.Event
	m       staff.Meta
}
