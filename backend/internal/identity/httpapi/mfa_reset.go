package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: отмена сброса 2FA владельцем — владелец Task 18 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1).

// CancelMyMfaReset — DELETE /v1/me/mfa-reset: игрок отменяет сброс 2FA через поддержку («это не я») — 204; сессия,
// вошедшая вторым фактором, сброс уже отменила — тоже 204; иначе 404 identity.mfa_reset_not_found.
func (h Handler) CancelMyMfaReset(ctx context.Context, _ oapi.CancelMyMfaResetRequestObject) (oapi.CancelMyMfaResetResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.CancelMyMfaReset(ctx, p.UserID, p.SessionID); err != nil {
		return nil, err
	}
	return oapi.CancelMyMfaReset204Response{}, nil
}
