package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: отмена сброса 2FA владельцем — владелец Task 18 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// CancelMyMfaReset — DELETE /v1/me/mfa-reset: отменить сброс 2FA через поддержку.
func (Handler) CancelMyMfaReset(context.Context, oapi.CancelMyMfaResetRequestObject) (oapi.CancelMyMfaResetResponseObject, error) {
	return nil, errNotImplemented
}
