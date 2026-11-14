package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: регистрация по почте, коды из письма, ник, документы — владелец Task 10 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// CheckNickname — GET /v1/nicknames/availability: доступность ника.
func (Handler) CheckNickname(context.Context, oapi.CheckNicknameRequestObject) (oapi.CheckNicknameResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmSignup — POST /v1/auth/signup/confirm: подтвердить регистрацию кодом.
func (Handler) ConfirmSignup(context.Context, oapi.ConfirmSignupRequestObject) (oapi.ConfirmSignupResponseObject, error) {
	return nil, errNotImplemented
}

// GetLegalDocuments — GET /v1/legal/documents: текущие версии документов.
func (Handler) GetLegalDocuments(context.Context, oapi.GetLegalDocumentsRequestObject) (oapi.GetLegalDocumentsResponseObject, error) {
	return nil, errNotImplemented
}

// ResendAuthCode — POST /v1/auth/codes/{codeId}/resend: отправить код повторно.
func (Handler) ResendAuthCode(context.Context, oapi.ResendAuthCodeRequestObject) (oapi.ResendAuthCodeResponseObject, error) {
	return nil, errNotImplemented
}

// StartSignup — POST /v1/auth/signup: начать регистрацию по почте.
func (Handler) StartSignup(context.Context, oapi.StartSignupRequestObject) (oapi.StartSignupResponseObject, error) {
	return nil, errNotImplemented
}
