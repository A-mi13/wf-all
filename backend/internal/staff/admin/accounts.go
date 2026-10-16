package admin

import (
	"context"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

// «Роли и доступ» (спека §4.9, §2.6): список и карточка сотрудников, приглашение, смена роли, выключение,
// сброс доступа, повторная отправка, чужие сессии, матрица ролей. Право и цель решает сценарий: Allow —
// сразу (200 с учёткой), NeedsApproval — запрос подтверждения (202 {approval}, ничего не исполнено), Deny —
// 403. Ошибки сценариев возвращаются как есть — их переводит Middleware (Problem).

// ListStaffAccounts — GET /v1/staff/accounts: страница сотрудников, новые сверху.
func (h *Handler) ListStaffAccounts(ctx context.Context, req oapi.ListStaffAccountsRequestObject) (oapi.ListStaffAccountsResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := staff.AccountFilter{Q: deref(p.Q), Cursor: deref(p.Cursor)}
	if p.Status != nil {
		f.Status = string(*p.Status)
	}
	if p.Role != nil {
		f.Role = string(*p.Role)
	}
	if p.Limit != nil {
		f.Limit = *p.Limit
	}
	views, next, err := h.app.Accounts(ctx, s, f)
	if err != nil {
		return nil, err
	}
	out := oapi.ListStaffAccounts200JSONResponse{Items: make([]oapi.StaffAccount, 0, len(views))}
	for _, v := range views {
		out.Items = append(out.Items, accountOut(v))
	}
	if next != "" {
		out.NextCursor = &next
	}
	return out, nil
}

// GetStaffAccount — GET /v1/staff/accounts/{staffId}: карточка; нет — staff.not_found.
func (h *Handler) GetStaffAccount(ctx context.Context, req oapi.GetStaffAccountRequestObject) (oapi.GetStaffAccountResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.Account(ctx, s, req.StaffId)
	if err != nil {
		return nil, err
	}
	return oapi.GetStaffAccount200JSONResponse(accountOut(v)), nil
}

// CreateStaffInvite — POST /v1/staff/invites: всегда запрос подтверждения (P13) — 202 {approval}.
func (h *Handler) CreateStaffInvite(ctx context.Context, req oapi.CreateStaffInviteRequestObject) (oapi.CreateStaffInviteResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := staff.InviteInput{Email: b.Email, DisplayName: b.DisplayName, Role: string(b.Role)}
	if b.CityIds != nil {
		in.CityIDs = *b.CityIds
	}
	ap, err := h.app.Invite(ctx, s, in, meta(ctx))
	if err != nil {
		return nil, err
	}
	body, err := accepted(ap)
	if err != nil {
		return nil, err
	}
	return oapi.CreateStaffInvite202JSONResponse(body), nil
}

// ResendStaffInvite — POST /v1/staff/accounts/{staffId}/invite/resend: новый код приглашения, 204; пауза
// 60 с от выпуска — 429 с Retry-After (ResendTooSoonError).
func (h *Handler) ResendStaffInvite(ctx context.Context, req oapi.ResendStaffInviteRequestObject) (oapi.ResendStaffInviteResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.ResendInvite(ctx, s, req.StaffId, meta(ctx)); err != nil {
		return nil, err
	}
	return oapi.ResendStaffInvite204Response{}, nil
}

// ChangeStaffRole — PUT /v1/staff/accounts/{staffId}/role: роль и города целиком (D16). Сразу (только снятие
// городов) или без изменений — 200 с учёткой; вдвоём — 202 {approval}. Версии в теле нет (контракт): правка
// из устаревшей панели — «последняя запись побеждает», след — аудит staff.role_changed (план A).
func (h *Handler) ChangeStaffRole(ctx context.Context, req oapi.ChangeStaffRoleRequestObject) (oapi.ChangeStaffRoleResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	ap, err := h.app.ChangeRole(ctx, s, req.StaffId, perm.Role(req.Body.Role), req.Body.CityIds, meta(ctx))
	if err != nil {
		return nil, err
	}
	if ap != nil {
		body, err := accepted(*ap)
		if err != nil {
			return nil, err
		}
		return oapi.ChangeStaffRole202JSONResponse(body), nil
	}
	v, err := h.app.Account(ctx, s, req.StaffId)
	if err != nil {
		return nil, err
	}
	return oapi.ChangeStaffRole200JSONResponse(accountOut(v)), nil
}

// DisableStaffAccount — POST /v1/staff/accounts/{staffId}/disable: снять роль. Модератор, поддержка, админ —
// сразу (200 с учёткой); владелец — вдвоём (202 {approval}).
func (h *Handler) DisableStaffAccount(ctx context.Context, req oapi.DisableStaffAccountRequestObject) (oapi.DisableStaffAccountResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	ap, err := h.app.Disable(ctx, s, req.StaffId, string(b.Reason), deref(b.Comment), b.Confirm, meta(ctx))
	if err != nil {
		return nil, err
	}
	if ap != nil {
		body, err := accepted(*ap)
		if err != nil {
			return nil, err
		}
		return oapi.DisableStaffAccount202JSONResponse(body), nil
	}
	v, err := h.app.Account(ctx, s, req.StaffId)
	if err != nil {
		return nil, err
	}
	return oapi.DisableStaffAccount200JSONResponse(accountOut(v)), nil
}

// ResetStaffAccess — POST /v1/staff/accounts/{staffId}/access-reset: всегда вдвоём — 202 {approval}.
func (h *Handler) ResetStaffAccess(ctx context.Context, req oapi.ResetStaffAccessRequestObject) (oapi.ResetStaffAccessResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	ap, err := h.app.ResetAccess(ctx, s, req.StaffId, string(b.Reason), deref(b.Comment), b.Confirm, meta(ctx))
	if err != nil {
		return nil, err
	}
	body, err := accepted(ap)
	if err != nil {
		return nil, err
	}
	return oapi.ResetStaffAccess202JSONResponse(body), nil
}

// ListStaffAccountSessions — GET /v1/staff/accounts/{staffId}/sessions: живые сессии сотрудника; админу —
// только модераторов и поддержки (сценарий: иначе 403).
func (h *Handler) ListStaffAccountSessions(ctx context.Context, req oapi.ListStaffAccountSessionsRequestObject) (oapi.ListStaffAccountSessionsResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	views, err := h.app.AccountSessions(ctx, s, req.StaffId)
	if err != nil {
		return nil, err
	}
	return oapi.ListStaffAccountSessions200JSONResponse{Items: sessionsOut(views)}, nil
}

// RevokeStaffAccountSessions — POST /v1/staff/accounts/{staffId}/sessions/revoke-all: сколько сессий
// завершено.
func (h *Handler) RevokeStaffAccountSessions(ctx context.Context, req oapi.RevokeStaffAccountSessionsRequestObject) (oapi.RevokeStaffAccountSessionsResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.app.RevokeAccountSessions(ctx, s, req.StaffId, meta(ctx))
	if err != nil {
		return nil, err
	}
	return oapi.RevokeStaffAccountSessions200JSONResponse{Revoked: n}, nil
}

// GetStaffRoles — GET /v1/staff/roles: роли по старшинству и матрица прав без черновых строк.
func (h *Handler) GetStaffRoles(ctx context.Context, _ oapi.GetStaffRolesRequestObject) (oapi.GetStaffRolesResponseObject, error) {
	if _, err := current(ctx); err != nil {
		return nil, err
	}
	v := h.app.RolesMatrix()
	out := oapi.GetStaffRoles200JSONResponse{
		Roles:       make([]oapi.StaffRole, 0, len(v.Roles)),
		Permissions: make([]oapi.RolePermission, 0, len(v.Permissions)),
	}
	for _, r := range v.Roles {
		out.Roles = append(out.Roles, oapi.StaffRole(r))
	}
	for _, p := range v.Permissions {
		out.Permissions = append(out.Permissions, oapi.RolePermission{Key: p.Key, Mutating: p.Mutating, Grants: oapi.RoleGrants{
			Owner:     oapi.RoleGrant(p.Grants[string(perm.Owner)]),
			Admin:     oapi.RoleGrant(p.Grants[string(perm.Admin)]),
			Moderator: oapi.RoleGrant(p.Grants[string(perm.Moderator)]),
			Support:   oapi.RoleGrant(p.Grants[string(perm.Support)]),
		}})
	}
	return out, nil
}

// accepted — 202 {approval}: действие не исполнено, создан запрос подтверждения.
func accepted(ap approvals.Approval) (oapi.ApprovalAccepted, error) {
	a, err := oapi.ApprovalOf(ap)
	if err != nil {
		return oapi.ApprovalAccepted{}, err
	}
	return oapi.ApprovalAccepted{Approval: a}, nil
}

// accountOut — StaffAccount: city_ids — всегда массив; времена — UTC; приглашение — без служебного
// IssuedAt (пауза повторной отправки считается на сервере).
func accountOut(v staff.AccountView) oapi.StaffAccount {
	out := oapi.StaffAccount{
		Id: v.ID, Email: v.Email, DisplayName: v.DisplayName, Locale: oapi.StaffLocale(v.Locale),
		Status: oapi.StaffStatus(v.Status), Role: oapi.StaffRole(v.Role),
		CityIds:   append(make([]uuid.UUID, 0, len(v.CityIDs)), v.CityIDs...),
		CreatedAt: v.CreatedAt.UTC(), LastLoginAt: utcPtr(v.LastLoginAt), LockedUntil: utcPtr(v.LockedUntil),
		Version: v.Version,
	}
	if i := v.Invite; i != nil {
		out.Invite = &oapi.StaffInviteState{Purpose: oapi.StaffInviteStatePurpose(i.Purpose), ExpiresAt: i.ExpiresAt.UTC(), Expired: i.Expired}
	}
	return out
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
