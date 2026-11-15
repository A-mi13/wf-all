package httpapi

import (
	"context"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
)

// Операции области: сброс и смена пароля, свежий вход, смена почты — владелец Task 13 плана I-2 (спека identity §3.4,
// §3.9, §3.10, §3.13). Нулевой Handler (без App) — 501.

// errPasswordNoPrincipal — операция с замком без принципала (конвейер до неё не пустит): 401, как мёртвая сессия.
var errPasswordNoPrincipal = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)

func passwordPrincipal(ctx context.Context) (*auth.Principal, error) {
	p, ok := auth.From(ctx)
	if !ok {
		return nil, errPasswordNoPrincipal
	}
	return p, nil
}

// StartPasswordReset — POST /v1/auth/password-reset: начать сброс пароля.
func (h Handler) StartPasswordReset(ctx context.Context, req oapi.StartPasswordResetRequestObject) (oapi.StartPasswordResetResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	sent, err := h.app.StartPasswordReset(ctx, req.Body.Email)
	if err != nil {
		return nil, err
	}
	return oapi.StartPasswordReset202JSONResponse{Body: codeSent(sent),
		Headers: oapi.StartPasswordReset202ResponseHeaders{CacheControl: codeCachePrivate}}, nil
}

// ConfirmPasswordReset — POST /v1/auth/password-reset/confirm: новый пароль по коду.
func (h Handler) ConfirmPasswordReset(ctx context.Context, req oapi.ConfirmPasswordResetRequestObject) (oapi.ConfirmPasswordResetResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	b := req.Body
	in := app.ResetInput{CodeID: b.CodeId, Code: b.Code, NewPassword: b.NewPassword}
	if b.Mfa != nil {
		in.MFA = &app.SecondFactor{}
		if b.Mfa.Code != nil {
			in.MFA.TOTP = *b.Mfa.Code
		}
		if b.Mfa.RecoveryCode != nil {
			in.MFA.Recovery = *b.Mfa.RecoveryCode
		}
	}
	out, err := h.app.ConfirmPasswordReset(ctx, in, ClientFrom(ctx, b.Device))
	if err != nil {
		return nil, err
	}
	docs := false
	if out.Status == app.StatusAuthenticated {
		if docs, err = h.app.DocumentsRequired(ctx, out.User.ID); err != nil {
			return nil, err
		}
	}
	return oapi.ConfirmPasswordReset200JSONResponse{Body: authResult(app.AuthOutcome{LoginOutcome: out, DocumentsRequired: docs}, false),
		Headers: oapi.ConfirmPasswordReset200ResponseHeaders{CacheControl: codeCacheNoStore}}, nil
}

// Reauthenticate — POST /v1/me/reauth: свежий вход.
func (h Handler) Reauthenticate(ctx context.Context, req oapi.ReauthenticateRequestObject) (oapi.ReauthenticateResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := app.ReauthInput{Passkey: b.Passkey != nil, Social: b.Social != nil}
	if b.Password != nil {
		in.Password = *b.Password
	}
	if b.TotpCode != nil {
		in.TOTPCode = *b.TotpCode
	}
	if b.RecoveryCode != nil {
		in.RecoveryCode = *b.RecoveryCode
	}
	until, err := h.app.Reauthenticate(ctx, p.SessionID, in)
	if err != nil {
		return nil, err
	}
	return oapi.Reauthenticate200JSONResponse{Body: oapi.ReauthResult{ReauthUntil: until},
		Headers: oapi.Reauthenticate200ResponseHeaders{CacheControl: codeCachePrivate}}, nil
}

// ChangePassword — POST /v1/me/password: сменить или задать пароль.
func (h Handler) ChangePassword(ctx context.Context, req oapi.ChangePasswordRequestObject) (oapi.ChangePasswordResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.ChangePassword(ctx, p.SessionID, req.Body.NewPassword, ClientFrom(ctx, nil)); err != nil {
		return nil, err
	}
	return oapi.ChangePassword204Response{}, nil
}

// StartEmailChange — POST /v1/me/email-change: начать смену почты.
func (h Handler) StartEmailChange(ctx context.Context, req oapi.StartEmailChangeRequestObject) (oapi.StartEmailChangeResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	sent, err := h.app.StartEmailChange(ctx, p.SessionID, req.Body.NewEmail, ClientFrom(ctx, nil))
	if err != nil {
		return nil, err
	}
	return oapi.StartEmailChange202JSONResponse{Body: codeSent(sent),
		Headers: oapi.StartEmailChange202ResponseHeaders{CacheControl: codeCachePrivate}}, nil
}

// ConfirmEmailChange — POST /v1/me/email-change/confirm: подтвердить новую почту.
func (h Handler) ConfirmEmailChange(ctx context.Context, req oapi.ConfirmEmailChangeRequestObject) (oapi.ConfirmEmailChangeResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	me, err := h.app.ConfirmEmailChange(ctx, p.UserID, req.Body.CodeId, req.Body.Code)
	if err != nil {
		return nil, err
	}
	return oapi.ConfirmEmailChange200JSONResponse{Body: AccountMe(me),
		Headers: oapi.ConfirmEmailChange200ResponseHeaders{CacheControl: codeCachePrivate}}, nil
}
