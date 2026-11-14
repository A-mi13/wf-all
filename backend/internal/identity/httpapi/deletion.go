package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: удаление аккаунта и его отмена — владелец Task 15 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// CancelDeletion — POST /v1/auth/deletion/cancel: отменить удаление аккаунта.
func (Handler) CancelDeletion(context.Context, oapi.CancelDeletionRequestObject) (oapi.CancelDeletionResponseObject, error) {
	return nil, errNotImplemented
}

// DeleteAccount — POST /v1/me/delete: удалить аккаунт.
func (Handler) DeleteAccount(context.Context, oapi.DeleteAccountRequestObject) (oapi.DeleteAccountResponseObject, error) {
	return nil, errNotImplemented
}
