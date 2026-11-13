// Package httpapi — публичные ручки модуля identity (тег identity контракта public, спека identity §6): вход и
// регистрация, аккаунт, сессии, 2FA, ключи доступа, соцвходы, капитанство. Handler встраивается в
// internal/httpapi/public.Server: тег операции = модуль-реализатор (apitest.TagViolations).
//
// План I-1 (контракт до кода): нулевое значение Handler — каркас, каждая операция отвечает 501
// http.not_implemented (общий код обоих контрактов, R1), пока этап спеки её не реализует; такие операции —
// в pendingOps стража TestNoStubOperations (internal/httpapi/public).
package httpapi

import (
	"context"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// Handler — ручки identity. Нулевое значение — заглушка: каждая операция отвечает 501, пока этап спеки её не
// реализует (тогда метод получает зависимости и сценарий, а операция уходит из pendingOps).
type Handler struct{}

// errNotImplemented — ответ операции, которую этап спеки ещё не реализовал.
var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

// Вход и регистрация, аккаунт, сессии (план I-1, Task 2; спека identity §6.3) — реализует этап 4 спеки.

// CheckNickname — GET /v1/nicknames/availability: доступность ника.
func (Handler) CheckNickname(context.Context, oapi.CheckNicknameRequestObject) (oapi.CheckNicknameResponseObject, error) {
	return nil, errNotImplemented
}

// StartSignup — POST /v1/auth/signup: начать регистрацию по почте.
func (Handler) StartSignup(context.Context, oapi.StartSignupRequestObject) (oapi.StartSignupResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmSignup — POST /v1/auth/signup/confirm: подтвердить регистрацию кодом.
func (Handler) ConfirmSignup(context.Context, oapi.ConfirmSignupRequestObject) (oapi.ConfirmSignupResponseObject, error) {
	return nil, errNotImplemented
}

// ResendAuthCode — POST /v1/auth/codes/{codeId}/resend: отправить код повторно.
func (Handler) ResendAuthCode(context.Context, oapi.ResendAuthCodeRequestObject) (oapi.ResendAuthCodeResponseObject, error) {
	return nil, errNotImplemented
}

// Login — POST /v1/auth/login: вход по почте и паролю.
func (Handler) Login(context.Context, oapi.LoginRequestObject) (oapi.LoginResponseObject, error) {
	return nil, errNotImplemented
}

// CompleteMfa — POST /v1/auth/mfa: второй фактор входа.
func (Handler) CompleteMfa(context.Context, oapi.CompleteMfaRequestObject) (oapi.CompleteMfaResponseObject, error) {
	return nil, errNotImplemented
}

// StartPasswordReset — POST /v1/auth/password-reset: начать сброс пароля.
func (Handler) StartPasswordReset(context.Context, oapi.StartPasswordResetRequestObject) (oapi.StartPasswordResetResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmPasswordReset — POST /v1/auth/password-reset/confirm: новый пароль по коду.
func (Handler) ConfirmPasswordReset(context.Context, oapi.ConfirmPasswordResetRequestObject) (oapi.ConfirmPasswordResetResponseObject, error) {
	return nil, errNotImplemented
}

// RefreshSession — POST /v1/auth/refresh: обновить токены.
func (Handler) RefreshSession(context.Context, oapi.RefreshSessionRequestObject) (oapi.RefreshSessionResponseObject, error) {
	return nil, errNotImplemented
}

// CancelDeletion — POST /v1/auth/deletion/cancel: отменить удаление аккаунта.
func (Handler) CancelDeletion(context.Context, oapi.CancelDeletionRequestObject) (oapi.CancelDeletionResponseObject, error) {
	return nil, errNotImplemented
}

// GetLegalDocuments — GET /v1/legal/documents: текущие версии документов.
func (Handler) GetLegalDocuments(context.Context, oapi.GetLegalDocumentsRequestObject) (oapi.GetLegalDocumentsResponseObject, error) {
	return nil, errNotImplemented
}

// CreateDeviceChallenge — POST /v1/devices/challenges: задача проверки целостности приложения.
func (Handler) CreateDeviceChallenge(context.Context, oapi.CreateDeviceChallengeRequestObject) (oapi.CreateDeviceChallengeResponseObject, error) {
	return nil, errNotImplemented
}

// Logout — POST /v1/auth/logout: выйти.
func (Handler) Logout(context.Context, oapi.LogoutRequestObject) (oapi.LogoutResponseObject, error) {
	return nil, errNotImplemented
}

// GetMe — GET /v1/me: мой аккаунт.
func (Handler) GetMe(context.Context, oapi.GetMeRequestObject) (oapi.GetMeResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateMe — PATCH /v1/me: настройки аккаунта.
func (Handler) UpdateMe(context.Context, oapi.UpdateMeRequestObject) (oapi.UpdateMeResponseObject, error) {
	return nil, errNotImplemented
}

// ChangeNickname — PATCH /v1/me/nickname: сменить ник.
func (Handler) ChangeNickname(context.Context, oapi.ChangeNicknameRequestObject) (oapi.ChangeNicknameResponseObject, error) {
	return nil, errNotImplemented
}

// ListMySessions — GET /v1/me/sessions: «Где вы вошли».
func (Handler) ListMySessions(context.Context, oapi.ListMySessionsRequestObject) (oapi.ListMySessionsResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeMySession — DELETE /v1/me/sessions/{sessionId}: выйти на устройстве.
func (Handler) RevokeMySession(context.Context, oapi.RevokeMySessionRequestObject) (oapi.RevokeMySessionResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeOtherSessions — POST /v1/me/sessions/revoke-others: выйти везде, кроме этого устройства.
func (Handler) RevokeOtherSessions(context.Context, oapi.RevokeOtherSessionsRequestObject) (oapi.RevokeOtherSessionsResponseObject, error) {
	return nil, errNotImplemented
}

// ChangePassword — POST /v1/me/password: сменить или задать пароль.
func (Handler) ChangePassword(context.Context, oapi.ChangePasswordRequestObject) (oapi.ChangePasswordResponseObject, error) {
	return nil, errNotImplemented
}

// StartEmailChange — POST /v1/me/email-change: начать смену почты.
func (Handler) StartEmailChange(context.Context, oapi.StartEmailChangeRequestObject) (oapi.StartEmailChangeResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmEmailChange — POST /v1/me/email-change/confirm: подтвердить новую почту.
func (Handler) ConfirmEmailChange(context.Context, oapi.ConfirmEmailChangeRequestObject) (oapi.ConfirmEmailChangeResponseObject, error) {
	return nil, errNotImplemented
}

// AcceptDocuments — POST /v1/me/documents: принять документы.
func (Handler) AcceptDocuments(context.Context, oapi.AcceptDocumentsRequestObject) (oapi.AcceptDocumentsResponseObject, error) {
	return nil, errNotImplemented
}

// DeleteAccount — POST /v1/me/delete: удалить аккаунт.
func (Handler) DeleteAccount(context.Context, oapi.DeleteAccountRequestObject) (oapi.DeleteAccountResponseObject, error) {
	return nil, errNotImplemented
}
