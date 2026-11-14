package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: мой аккаунт, ник, документы, мои способы входа — владелец Task 14 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// AcceptDocuments — POST /v1/me/documents: принять документы.
func (Handler) AcceptDocuments(context.Context, oapi.AcceptDocumentsRequestObject) (oapi.AcceptDocumentsResponseObject, error) {
	return nil, errNotImplemented
}

// ChangeNickname — PATCH /v1/me/nickname: сменить ник.
func (Handler) ChangeNickname(context.Context, oapi.ChangeNicknameRequestObject) (oapi.ChangeNicknameResponseObject, error) {
	return nil, errNotImplemented
}

// GetMe — GET /v1/me: мой аккаунт.
func (Handler) GetMe(context.Context, oapi.GetMeRequestObject) (oapi.GetMeResponseObject, error) {
	return nil, errNotImplemented
}

// ListMyAuthMethods — GET /v1/me/auth-methods: мои способы входа.
func (Handler) ListMyAuthMethods(context.Context, oapi.ListMyAuthMethodsRequestObject) (oapi.ListMyAuthMethodsResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateMe — PATCH /v1/me: настройки аккаунта.
func (Handler) UpdateMe(context.Context, oapi.UpdateMeRequestObject) (oapi.UpdateMeResponseObject, error) {
	return nil, errNotImplemented
}
