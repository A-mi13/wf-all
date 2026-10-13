// Package admin — операции модуля staff в админке (тег staff контракта admin): вход, второй фактор,
// сессии, «Мой доступ», приглашения, роли и доступ сотрудников (спека сотрудников §4).
package admin

import (
	"context"
	"net/http"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
)

// Handler — операции тега staff; встраивается в internal/httpapi/admin.Server.
//
// Заглушки Task 9a: контракт admin заморожен раньше сценариев (план A, P16), strict-интерфейс
// собирается, а операции отвечают 501 http.not_implemented, пока задачи 10a–10b не заменят их
// хендлерами. Новую заглушку сюда не добавлять — файл удаляется вместе с последней.
type Handler struct{}

var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

// ChangeStaffPassword — POST /v1/staff/me/password.
func (Handler) ChangeStaffPassword(context.Context, oapi.ChangeStaffPasswordRequestObject) (oapi.ChangeStaffPasswordResponseObject, error) {
	return nil, errNotImplemented
}

// ChangeStaffRole — PUT /v1/staff/accounts/{staffId}/role.
func (Handler) ChangeStaffRole(context.Context, oapi.ChangeStaffRoleRequestObject) (oapi.ChangeStaffRoleResponseObject, error) {
	return nil, errNotImplemented
}

// CreateStaffInvite — POST /v1/staff/invites.
func (Handler) CreateStaffInvite(context.Context, oapi.CreateStaffInviteRequestObject) (oapi.CreateStaffInviteResponseObject, error) {
	return nil, errNotImplemented
}

// DisableStaffAccount — POST /v1/staff/accounts/{staffId}/disable.
func (Handler) DisableStaffAccount(context.Context, oapi.DisableStaffAccountRequestObject) (oapi.DisableStaffAccountResponseObject, error) {
	return nil, errNotImplemented
}

// GetStaffAccount — GET /v1/staff/accounts/{staffId}.
func (Handler) GetStaffAccount(context.Context, oapi.GetStaffAccountRequestObject) (oapi.GetStaffAccountResponseObject, error) {
	return nil, errNotImplemented
}

// GetStaffMe — GET /v1/staff/me.
func (Handler) GetStaffMe(context.Context, oapi.GetStaffMeRequestObject) (oapi.GetStaffMeResponseObject, error) {
	return nil, errNotImplemented
}

// GetStaffMfa — GET /v1/staff/me/mfa.
func (Handler) GetStaffMfa(context.Context, oapi.GetStaffMfaRequestObject) (oapi.GetStaffMfaResponseObject, error) {
	return nil, errNotImplemented
}

// GetStaffRoles — GET /v1/staff/roles.
func (Handler) GetStaffRoles(context.Context, oapi.GetStaffRolesRequestObject) (oapi.GetStaffRolesResponseObject, error) {
	return nil, errNotImplemented
}

// ListStaffAccounts — GET /v1/staff/accounts.
func (Handler) ListStaffAccounts(context.Context, oapi.ListStaffAccountsRequestObject) (oapi.ListStaffAccountsResponseObject, error) {
	return nil, errNotImplemented
}

// ListStaffAccountSessions — GET /v1/staff/accounts/{staffId}/sessions.
func (Handler) ListStaffAccountSessions(context.Context, oapi.ListStaffAccountSessionsRequestObject) (oapi.ListStaffAccountSessionsResponseObject, error) {
	return nil, errNotImplemented
}

// ListStaffMySessions — GET /v1/staff/me/sessions.
func (Handler) ListStaffMySessions(context.Context, oapi.ListStaffMySessionsRequestObject) (oapi.ListStaffMySessionsResponseObject, error) {
	return nil, errNotImplemented
}

// RegenerateStaffRecoveryCodes — POST /v1/staff/me/mfa/recovery-codes.
func (Handler) RegenerateStaffRecoveryCodes(context.Context, oapi.RegenerateStaffRecoveryCodesRequestObject) (oapi.RegenerateStaffRecoveryCodesResponseObject, error) {
	return nil, errNotImplemented
}

// ResendStaffInvite — POST /v1/staff/accounts/{staffId}/invite/resend.
func (Handler) ResendStaffInvite(context.Context, oapi.ResendStaffInviteRequestObject) (oapi.ResendStaffInviteResponseObject, error) {
	return nil, errNotImplemented
}

// ResetStaffAccess — POST /v1/staff/accounts/{staffId}/access-reset.
func (Handler) ResetStaffAccess(context.Context, oapi.ResetStaffAccessRequestObject) (oapi.ResetStaffAccessResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeStaffAccountSessions — POST /v1/staff/accounts/{staffId}/sessions/revoke-all.
func (Handler) RevokeStaffAccountSessions(context.Context, oapi.RevokeStaffAccountSessionsRequestObject) (oapi.RevokeStaffAccountSessionsResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeStaffMySession — DELETE /v1/staff/me/sessions/{sessionId}.
func (Handler) RevokeStaffMySession(context.Context, oapi.RevokeStaffMySessionRequestObject) (oapi.RevokeStaffMySessionResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeStaffOtherSessions — POST /v1/staff/me/sessions/revoke-others.
func (Handler) RevokeStaffOtherSessions(context.Context, oapi.RevokeStaffOtherSessionsRequestObject) (oapi.RevokeStaffOtherSessionsResponseObject, error) {
	return nil, errNotImplemented
}

// StaffAcceptInvite — POST /v1/staff/auth/invite/accept.
func (Handler) StaffAcceptInvite(context.Context, oapi.StaffAcceptInviteRequestObject) (oapi.StaffAcceptInviteResponseObject, error) {
	return nil, errNotImplemented
}

// StaffLogin — POST /v1/staff/auth/login.
func (Handler) StaffLogin(context.Context, oapi.StaffLoginRequestObject) (oapi.StaffLoginResponseObject, error) {
	return nil, errNotImplemented
}

// StaffLogout — POST /v1/staff/auth/logout.
func (Handler) StaffLogout(context.Context, oapi.StaffLogoutRequestObject) (oapi.StaffLogoutResponseObject, error) {
	return nil, errNotImplemented
}

// StaffMfaConfirm — POST /v1/staff/me/mfa/confirm.
func (Handler) StaffMfaConfirm(context.Context, oapi.StaffMfaConfirmRequestObject) (oapi.StaffMfaConfirmResponseObject, error) {
	return nil, errNotImplemented
}

// StaffMfaSetup — POST /v1/staff/me/mfa/setup.
func (Handler) StaffMfaSetup(context.Context, oapi.StaffMfaSetupRequestObject) (oapi.StaffMfaSetupResponseObject, error) {
	return nil, errNotImplemented
}

// StaffRecoveryAck — POST /v1/staff/me/mfa/recovery-codes/ack.
func (Handler) StaffRecoveryAck(context.Context, oapi.StaffRecoveryAckRequestObject) (oapi.StaffRecoveryAckResponseObject, error) {
	return nil, errNotImplemented
}

// StaffResendInviteSelf — POST /v1/staff/auth/invite/resend.
func (Handler) StaffResendInviteSelf(context.Context, oapi.StaffResendInviteSelfRequestObject) (oapi.StaffResendInviteSelfResponseObject, error) {
	return nil, errNotImplemented
}

// StaffUnlock — POST /v1/staff/auth/unlock.
func (Handler) StaffUnlock(context.Context, oapi.StaffUnlockRequestObject) (oapi.StaffUnlockResponseObject, error) {
	return nil, errNotImplemented
}

// StaffVerifyMfa — POST /v1/staff/auth/mfa.
func (Handler) StaffVerifyMfa(context.Context, oapi.StaffVerifyMfaRequestObject) (oapi.StaffVerifyMfaResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateStaffMeLocale — PATCH /v1/staff/me/locale.
func (Handler) UpdateStaffMeLocale(context.Context, oapi.UpdateStaffMeLocaleRequestObject) (oapi.UpdateStaffMeLocaleResponseObject, error) {
	return nil, errNotImplemented
}
