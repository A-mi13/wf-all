package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
)

// Операции области: 2FA игрока и второй шаг входа — владелец Task 16 плана I-2 (спека identity §3.3). Нулевой
// Handler (без App) — 501. Ответы с секретом (setupTotp, confirmTotp, regenerateRecoveryCodes) — Cache-Control:
// no-store ставит хендлер; не сохранять их в ключе идемпотентности — конвейер по x-idempotency: no-store маршрута.

// mfaNoStore — ответы с секретом TOTP, резервными кодами и токенами (контракт: CacheNoStore).
const mfaNoStore = "no-store"

// CompleteMfa — POST /v1/auth/mfa: второй фактор входа.
func (h Handler) CompleteMfa(ctx context.Context, req oapi.CompleteMfaRequestObject) (oapi.CompleteMfaResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	b := req.Body
	var f app.SecondFactor
	if b.Code != nil {
		f.TOTP = *b.Code
	}
	if b.RecoveryCode != nil {
		f.Recovery = *b.RecoveryCode
	}
	var pk *app.PasskeyInput
	if b.Passkey != nil {
		pk = &app.PasskeyInput{}
	}
	out, err := h.app.CompleteMfa(ctx, b.MfaToken, f, pk, ClientFrom(ctx, b.Device))
	if err != nil {
		return nil, err
	}
	// флаг документов посчитан в транзакции шага — после коммита база не читается
	return oapi.CompleteMfa200JSONResponse{Body: authResult(out, false),
		Headers: oapi.CompleteMfa200ResponseHeaders{CacheControl: mfaNoStore}}, nil
}

// ConfirmTotp — POST /v1/me/mfa/totp/confirm: подтвердить включение 2FA.
func (h Handler) ConfirmTotp(ctx context.Context, req oapi.ConfirmTotpRequestObject) (oapi.ConfirmTotpResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := h.app.ConfirmTotp(ctx, p.UserID, req.Body.Code)
	if err != nil {
		return nil, err
	}
	return oapi.ConfirmTotp200JSONResponse{Body: oapi.RecoveryCodes{RecoveryCodes: codes},
		Headers: oapi.ConfirmTotp200ResponseHeaders{CacheControl: mfaNoStore}}, nil
}

// DisableTotp — DELETE /v1/me/mfa/totp: выключить 2FA.
func (h Handler) DisableTotp(ctx context.Context, _ oapi.DisableTotpRequestObject) (oapi.DisableTotpResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.DisableTotp(ctx, p.SessionID); err != nil {
		return nil, err
	}
	return oapi.DisableTotp204Response{}, nil
}

// GetMyMfa — GET /v1/me/mfa: состояние 2FA.
func (h Handler) GetMyMfa(ctx context.Context, _ oapi.GetMyMfaRequestObject) (oapi.GetMyMfaResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := h.app.MfaStatus(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return oapi.GetMyMfa200JSONResponse{Body: oapi.MfaStatus{TotpEnabled: st.TOTPEnabled, ConfirmedAt: st.ConfirmedAt,
		RecoveryCodesLeft: st.RecoveryCodesLeft, ResetScheduledAt: st.ResetScheduledAt},
		Headers: oapi.GetMyMfa200ResponseHeaders{CacheControl: accountCachePrivate}}, nil
}

// RegenerateRecoveryCodes — POST /v1/me/mfa/recovery-codes: новые резервные коды.
func (h Handler) RegenerateRecoveryCodes(ctx context.Context, _ oapi.RegenerateRecoveryCodesRequestObject) (oapi.RegenerateRecoveryCodesResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := h.app.RegenerateRecoveryCodes(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}
	return oapi.RegenerateRecoveryCodes200JSONResponse{Body: oapi.RecoveryCodes{RecoveryCodes: codes},
		Headers: oapi.RegenerateRecoveryCodes200ResponseHeaders{CacheControl: mfaNoStore}}, nil
}

// SetupTotp — POST /v1/me/mfa/totp/setup: начать включение 2FA.
func (h Handler) SetupTotp(ctx context.Context, _ oapi.SetupTotpRequestObject) (oapi.SetupTotpResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := passwordPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	secret, uri, err := h.app.SetupTotp(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}
	return oapi.SetupTotp200JSONResponse{Body: oapi.TotpSetup{Secret: secret, OtpauthUri: uri},
		Headers: oapi.SetupTotp200ResponseHeaders{CacheControl: mfaNoStore}}, nil
}
