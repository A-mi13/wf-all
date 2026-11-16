package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: соцвходы, ключи доступа, аттестация — план I-3 (заглушки 501) (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// CompleteSocialSignup — POST /v1/auth/social/signup: дорегистрация после Apple или Google.
func (Handler) CompleteSocialSignup(context.Context, oapi.CompleteSocialSignupRequestObject) (oapi.CompleteSocialSignupResponseObject, error) {
	return nil, errNotImplemented
}

// CreateDeviceChallenge — POST /v1/devices/challenges: задача проверки целостности приложения.
func (Handler) CreateDeviceChallenge(context.Context, oapi.CreateDeviceChallengeRequestObject) (oapi.CreateDeviceChallengeResponseObject, error) {
	return nil, errNotImplemented
}

// CreateSocialNonce — POST /v1/auth/social/nonces: nonce для входа через Apple или Google.
func (Handler) CreateSocialNonce(context.Context, oapi.CreateSocialNonceRequestObject) (oapi.CreateSocialNonceResponseObject, error) {
	return nil, errNotImplemented
}

// DeletePasskey — DELETE /v1/me/passkeys/{passkeyId}: удалить ключ доступа.
func (Handler) DeletePasskey(context.Context, oapi.DeletePasskeyRequestObject) (oapi.DeletePasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// LinkIdentity — POST /v1/me/identities: привязать Apple или Google.
func (Handler) LinkIdentity(context.Context, oapi.LinkIdentityRequestObject) (oapi.LinkIdentityResponseObject, error) {
	return nil, errNotImplemented
}

// PasskeyLogin — POST /v1/auth/passkey: войти ключом доступа.
func (Handler) PasskeyLogin(context.Context, oapi.PasskeyLoginRequestObject) (oapi.PasskeyLoginResponseObject, error) {
	return nil, errNotImplemented
}

// RegisterPasskey — POST /v1/me/passkeys: добавить ключ доступа.
func (Handler) RegisterPasskey(context.Context, oapi.RegisterPasskeyRequestObject) (oapi.RegisterPasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// RenamePasskey — PATCH /v1/me/passkeys/{passkeyId}: переименовать ключ доступа.
func (Handler) RenamePasskey(context.Context, oapi.RenamePasskeyRequestObject) (oapi.RenamePasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// SocialLogin — POST /v1/auth/social: войти через Apple или Google.
func (Handler) SocialLogin(context.Context, oapi.SocialLoginRequestObject) (oapi.SocialLoginResponseObject, error) {
	return nil, errNotImplemented
}

// StartMfaPasskey — POST /v1/auth/mfa/passkey-options: задача WebAuthn для второго фактора.
func (Handler) StartMfaPasskey(context.Context, oapi.StartMfaPasskeyRequestObject) (oapi.StartMfaPasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// StartPasskeyLogin — POST /v1/auth/passkey/options: задача WebAuthn для входа ключом.
func (Handler) StartPasskeyLogin(context.Context, oapi.StartPasskeyLoginRequestObject) (oapi.StartPasskeyLoginResponseObject, error) {
	return nil, errNotImplemented
}

// StartPasskeyRegistration — POST /v1/me/passkeys/registration-options: задача WebAuthn для нового ключа.
func (Handler) StartPasskeyRegistration(context.Context, oapi.StartPasskeyRegistrationRequestObject) (oapi.StartPasskeyRegistrationResponseObject, error) {
	return nil, errNotImplemented
}

// StartReauthPasskey — POST /v1/me/reauth/passkey-options: задача WebAuthn для свежего входа.
func (Handler) StartReauthPasskey(context.Context, oapi.StartReauthPasskeyRequestObject) (oapi.StartReauthPasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// UnlinkIdentity — DELETE /v1/me/identities/{identityId}: отвязать Apple или Google.
func (Handler) UnlinkIdentity(context.Context, oapi.UnlinkIdentityRequestObject) (oapi.UnlinkIdentityResponseObject, error) {
	return nil, errNotImplemented
}
