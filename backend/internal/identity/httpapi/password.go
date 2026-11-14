package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: сброс и смена пароля, свежий вход, смена почты — владелец Task 13 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// ChangePassword — POST /v1/me/password: сменить или задать пароль.
func (Handler) ChangePassword(context.Context, oapi.ChangePasswordRequestObject) (oapi.ChangePasswordResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmEmailChange — POST /v1/me/email-change/confirm: подтвердить новую почту.
func (Handler) ConfirmEmailChange(context.Context, oapi.ConfirmEmailChangeRequestObject) (oapi.ConfirmEmailChangeResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmPasswordReset — POST /v1/auth/password-reset/confirm: новый пароль по коду.
func (Handler) ConfirmPasswordReset(context.Context, oapi.ConfirmPasswordResetRequestObject) (oapi.ConfirmPasswordResetResponseObject, error) {
	return nil, errNotImplemented
}

// Reauthenticate — POST /v1/me/reauth: свежий вход.
func (Handler) Reauthenticate(context.Context, oapi.ReauthenticateRequestObject) (oapi.ReauthenticateResponseObject, error) {
	return nil, errNotImplemented
}

// StartEmailChange — POST /v1/me/email-change: начать смену почты.
func (Handler) StartEmailChange(context.Context, oapi.StartEmailChangeRequestObject) (oapi.StartEmailChangeResponseObject, error) {
	return nil, errNotImplemented
}

// StartPasswordReset — POST /v1/auth/password-reset: начать сброс пароля.
func (Handler) StartPasswordReset(context.Context, oapi.StartPasswordResetRequestObject) (oapi.StartPasswordResetResponseObject, error) {
	return nil, errNotImplemented
}
