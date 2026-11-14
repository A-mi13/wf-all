package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: 2FA игрока и второй шаг входа — владелец Task 16 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// CompleteMfa — POST /v1/auth/mfa: второй фактор входа.
func (Handler) CompleteMfa(context.Context, oapi.CompleteMfaRequestObject) (oapi.CompleteMfaResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmTotp — POST /v1/me/mfa/totp/confirm: подтвердить включение 2FA.
func (Handler) ConfirmTotp(context.Context, oapi.ConfirmTotpRequestObject) (oapi.ConfirmTotpResponseObject, error) {
	return nil, errNotImplemented
}

// DisableTotp — DELETE /v1/me/mfa/totp: выключить 2FA.
func (Handler) DisableTotp(context.Context, oapi.DisableTotpRequestObject) (oapi.DisableTotpResponseObject, error) {
	return nil, errNotImplemented
}

// GetMyMfa — GET /v1/me/mfa: состояние 2FA.
func (Handler) GetMyMfa(context.Context, oapi.GetMyMfaRequestObject) (oapi.GetMyMfaResponseObject, error) {
	return nil, errNotImplemented
}

// RegenerateRecoveryCodes — POST /v1/me/mfa/recovery-codes: новые резервные коды.
func (Handler) RegenerateRecoveryCodes(context.Context, oapi.RegenerateRecoveryCodesRequestObject) (oapi.RegenerateRecoveryCodesResponseObject, error) {
	return nil, errNotImplemented
}

// SetupTotp — POST /v1/me/mfa/totp/setup: начать включение 2FA.
func (Handler) SetupTotp(context.Context, oapi.SetupTotpRequestObject) (oapi.SetupTotpResponseObject, error) {
	return nil, errNotImplemented
}
