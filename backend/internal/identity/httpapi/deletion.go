package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: удаление аккаунта и его отмена — Task 15 плана I-2 (раскладка — Task 8, R5, Приложение 1).
// Нулевой Handler — 501 (TestZeroHandlerNotImplemented).

// deletionCacheNoStore — Cache-Control ответа с токенами (CacheNoStore контракта).
const deletionCacheNoStore = "no-store"

// CancelDeletion — POST /v1/auth/deletion/cancel: отменить удаление аккаунта (анонимно, по restore_token).
func (h Handler) CancelDeletion(ctx context.Context, req oapi.CancelDeletionRequestObject) (oapi.CancelDeletionResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	out, err := h.app.CancelDeletion(ctx, req.Body.RestoreToken, ClientFrom(ctx, req.Body.Device))
	if err != nil {
		return nil, err
	}
	return oapi.CancelDeletion200JSONResponse{Body: authResult(out, false),
		Headers: oapi.CancelDeletion200ResponseHeaders{CacheControl: deletionCacheNoStore}}, nil
}

// DeleteAccount — POST /v1/me/delete: удалить аккаунт (период отмены 30 дней).
func (h Handler) DeleteAccount(ctx context.Context, req oapi.DeleteAccountRequestObject) (oapi.DeleteAccountResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	purgeAt, err := h.app.DeleteAccount(ctx, p.SessionID, req.Body.ConfirmNickname)
	if err != nil {
		return nil, err
	}
	return oapi.DeleteAccount200JSONResponse{Body: oapi.DeletionScheduled{PurgeAt: purgeAt.UTC()},
		Headers: oapi.DeleteAccount200ResponseHeaders{CacheControl: accountCachePrivate}}, nil
}
