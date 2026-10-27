package admin

import (
	"context"
	"net/http"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
)

// Вход, второй фактор, выход, принятие приглашения и настройка 2FA (спека §4.4–4.7). Ошибки сценариев
// возвращаются как есть — их переводит Middleware (Problem).

// StaffLogin — POST /v1/staff/auth/login (анонимно): почта и пароль → сессия mfa_pending, новый токен
// в cookie. Сессию старой cookie (сотрудник конвейера в ctx) сценарий гасит как replaced в той же
// транзакции, что создаёт новую (D9).
func (h *Handler) StaffLogin(ctx context.Context, req oapi.StaffLoginRequestObject) (oapi.StaffLoginResponseObject, error) {
	res, err := h.app.Login(ctx, req.Body.Email, req.Body.Password, meta(ctx))
	if err != nil {
		return nil, err
	}
	return h.withToken(res.Token, http.StatusOK, oapi.StaffStageResult{Stage: oapi.StaffStage(res.Stage)}), nil
}

// StaffVerifyMfa — POST /v1/staff/auth/mfa: второй фактор входа (стадия mfa_pending) → новый токен
// стадии active, 204.
func (h *Handler) StaffVerifyMfa(ctx context.Context, req oapi.StaffVerifyMfaRequestObject) (oapi.StaffVerifyMfaResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := h.app.VerifyMFA(ctx, s, deref(req.Body.Code), deref(req.Body.RecoveryCode), meta(ctx))
	if err != nil {
		return nil, err
	}
	return h.withToken(tok, http.StatusNoContent, nil), nil
}

// StaffUnlock — POST /v1/staff/auth/unlock: второй фактор после auth.reauth_required → новый токен, 204.
func (h *Handler) StaffUnlock(ctx context.Context, req oapi.StaffUnlockRequestObject) (oapi.StaffUnlockResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := h.app.Unlock(ctx, s, deref(req.Body.Code), deref(req.Body.RecoveryCode), meta(ctx))
	if err != nil {
		return nil, err
	}
	return h.withToken(tok, http.StatusNoContent, nil), nil
}

// StaffLogout — POST /v1/staff/auth/logout: гасит текущую сессию в любой стадии и стирает cookie, 204.
func (h *Handler) StaffLogout(ctx context.Context, _ oapi.StaffLogoutRequestObject) (oapi.StaffLogoutResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.Logout(ctx, s); err != nil {
		return nil, err
	}
	return cookieResponse{cookie: h.clearCookie(), status: http.StatusNoContent}, nil
}

// StaffAcceptInvite — POST /v1/staff/auth/invite/accept (анонимно): почта, код и новый пароль → сессия
// setup, новый токен в cookie. Токен cookie запроса уходит сценарию (oldToken): её живая сессия —
// чья угодно — гасится (D9).
func (h *Handler) StaffAcceptInvite(ctx context.Context, req oapi.StaffAcceptInviteRequestObject) (oapi.StaffAcceptInviteResponseObject, error) {
	tok, err := h.app.AcceptInvite(ctx, req.Body.Email, req.Body.Code, req.Body.Password, requestFrom(ctx).token, meta(ctx))
	if err != nil {
		return nil, err
	}
	return h.withToken(tok, http.StatusOK, oapi.StaffStageResult{Stage: auth.StageSetup}), nil
}

// StaffResendInviteSelf — POST /v1/staff/auth/invite/resend (анонимно): всегда 202 — по ответу не
// узнать, есть ли почта; письмо с тем же кодом уходит, только если его можно выслать (пауза 60 с,
// не больше 5 отправок). 429 — только IP-лимит конвейера.
func (h *Handler) StaffResendInviteSelf(ctx context.Context, req oapi.StaffResendInviteSelfRequestObject) (oapi.StaffResendInviteSelfResponseObject, error) {
	if err := h.app.ResendInviteSelf(ctx, req.Body.Email, meta(ctx)); err != nil {
		return nil, err
	}
	return oapi.StaffResendInviteSelf202Response{}, nil
}

// StaffMfaSetup — POST /v1/staff/me/mfa/setup (стадия setup): новый секрет TOTP и otpauth-ссылка.
// Ответ с секретом: Cache-Control: no-store, в idempotency_keys не сохраняется (x-idempotency).
func (h *Handler) StaffMfaSetup(ctx context.Context, _ oapi.StaffMfaSetupRequestObject) (oapi.StaffMfaSetupResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	secret, uri, err := h.app.MFASetup(ctx, s)
	if err != nil {
		return nil, err
	}
	return oapi.StaffMfaSetup200JSONResponse{
		Body:    oapi.StaffMfaSecret{Secret: secret, OtpauthUri: uri},
		Headers: oapi.StaffMfaSetup200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// StaffMfaConfirm — POST /v1/staff/me/mfa/confirm (стадия setup): код из приложения → 10 резервных
// кодов один раз; no-store, как у секрета.
func (h *Handler) StaffMfaConfirm(ctx context.Context, req oapi.StaffMfaConfirmRequestObject) (oapi.StaffMfaConfirmResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := h.app.MFAConfirm(ctx, s, req.Body.Code, meta(ctx))
	if err != nil {
		return nil, err
	}
	return oapi.StaffMfaConfirm200JSONResponse{
		Body:    oapi.StaffRecoveryCodes{RecoveryCodes: codes},
		Headers: oapi.StaffMfaConfirm200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// StaffRecoveryAck — POST /v1/staff/me/mfa/recovery-codes/ack (стадия setup, 2FA подтверждена):
// учётка active, новый токен стадии active, 204.
func (h *Handler) StaffRecoveryAck(ctx context.Context, _ oapi.StaffRecoveryAckRequestObject) (oapi.StaffRecoveryAckResponseObject, error) {
	s, err := current(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := h.app.RecoveryAck(ctx, s, meta(ctx))
	if err != nil {
		return nil, err
	}
	return h.withToken(tok, http.StatusNoContent, nil), nil
}
