package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
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
	return oapi.CancelDeletion200JSONResponse{Body: deletionAuthResult(out),
		Headers: oapi.CancelDeletion200ResponseHeaders{CacheControl: deletionCacheNoStore}}, nil
}

// deletionAuthResult — AuthResult отмены удаления: сессия (documents_acceptance_required посчитан в транзакции отмены,
// m1 — после коммита база не читается) или, в защиту в глубину, шаг 2FA.
func deletionAuthResult(out app.CancelOutcome) oapi.AuthResult {
	r := oapi.AuthResult{Status: oapi.AuthStatus(out.Status)}
	switch out.Status {
	case app.StatusAuthenticated:
		t := out.Issued.Tokens
		r.Tokens = &oapi.TokenPair{AccessToken: t.Access, AccessExpiresAt: t.AccessExpires, RefreshToken: t.Refresh,
			RefreshIdleExpiresAt: t.RefreshIdleExpires, SessionId: t.SessionID}
		r.User = &oapi.AuthUser{Id: out.User.ID, Nickname: out.User.Nickname,
			DocumentsAcceptanceRequired: out.DocumentsRequired}
	case app.StatusMfaRequired:
		methods := make([]oapi.MfaMethod, 0, len(out.Methods))
		for _, m := range out.Methods {
			methods = append(methods, oapi.MfaMethod(m))
		}
		r.Mfa = &oapi.MfaChallenge{MfaToken: out.MfaToken, ExpiresAt: out.MfaExpires, Methods: methods, ResetAt: out.ResetAt}
	}
	return r
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
