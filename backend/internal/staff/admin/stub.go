package admin

import (
	"context"

	"wf/backend/internal/httpapi/admin/oapi"
)

// Заглушки Task 9a: контракт admin заморожен раньше сценариев (план A, P16), strict-интерфейс
// собирается, а операции управления сотрудниками отвечают 501 http.not_implemented, пока Task 10b
// не заменит их хендлерами. Новую заглушку сюда не добавлять — файл удаляется вместе с последней.

// ChangeStaffRole — PUT /v1/staff/accounts/{staffId}/role.
func (*Handler) ChangeStaffRole(context.Context, oapi.ChangeStaffRoleRequestObject) (oapi.ChangeStaffRoleResponseObject, error) {
	return nil, errNotImplemented
}

// CreateStaffInvite — POST /v1/staff/invites.
func (*Handler) CreateStaffInvite(context.Context, oapi.CreateStaffInviteRequestObject) (oapi.CreateStaffInviteResponseObject, error) {
	return nil, errNotImplemented
}

// DisableStaffAccount — POST /v1/staff/accounts/{staffId}/disable.
func (*Handler) DisableStaffAccount(context.Context, oapi.DisableStaffAccountRequestObject) (oapi.DisableStaffAccountResponseObject, error) {
	return nil, errNotImplemented
}

// GetStaffAccount — GET /v1/staff/accounts/{staffId}.
func (*Handler) GetStaffAccount(context.Context, oapi.GetStaffAccountRequestObject) (oapi.GetStaffAccountResponseObject, error) {
	return nil, errNotImplemented
}

// GetStaffRoles — GET /v1/staff/roles.
func (*Handler) GetStaffRoles(context.Context, oapi.GetStaffRolesRequestObject) (oapi.GetStaffRolesResponseObject, error) {
	return nil, errNotImplemented
}

// ListStaffAccounts — GET /v1/staff/accounts.
func (*Handler) ListStaffAccounts(context.Context, oapi.ListStaffAccountsRequestObject) (oapi.ListStaffAccountsResponseObject, error) {
	return nil, errNotImplemented
}

// ListStaffAccountSessions — GET /v1/staff/accounts/{staffId}/sessions.
func (*Handler) ListStaffAccountSessions(context.Context, oapi.ListStaffAccountSessionsRequestObject) (oapi.ListStaffAccountSessionsResponseObject, error) {
	return nil, errNotImplemented
}

// ResendStaffInvite — POST /v1/staff/accounts/{staffId}/invite/resend.
func (*Handler) ResendStaffInvite(context.Context, oapi.ResendStaffInviteRequestObject) (oapi.ResendStaffInviteResponseObject, error) {
	return nil, errNotImplemented
}

// ResetStaffAccess — POST /v1/staff/accounts/{staffId}/access-reset.
func (*Handler) ResetStaffAccess(context.Context, oapi.ResetStaffAccessRequestObject) (oapi.ResetStaffAccessResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeStaffAccountSessions — POST /v1/staff/accounts/{staffId}/sessions/revoke-all.
func (*Handler) RevokeStaffAccountSessions(context.Context, oapi.RevokeStaffAccountSessionsRequestObject) (oapi.RevokeStaffAccountSessionsResponseObject, error) {
	return nil, errNotImplemented
}
