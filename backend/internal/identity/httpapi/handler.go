// Package httpapi — публичные ручки модуля identity (тег identity контракта public, спека identity §6): вход и
// регистрация, аккаунт, сессии, 2FA, ключи доступа, соцвходы, капитанство. Handler встраивается в
// internal/httpapi/public.Server: тег операции = модуль-реализатор (apitest.TagViolations).
//
// План I-1 (контракт до кода): нулевое значение Handler — каркас, каждая операция отвечает 501
// http.not_implemented (общий код обоих контрактов, R1), пока этап спеки её не реализует; такие операции —
// в pendingOps стража TestNoStubOperations (internal/httpapi/public).
package httpapi

import (
	"context"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// Handler — ручки identity. Нулевое значение — заглушка: каждая операция отвечает 501, пока этап спеки её не
// реализует (тогда метод получает зависимости и сценарий, а операция уходит из pendingOps).
type Handler struct{}

// errNotImplemented — ответ операции, которую этап спеки ещё не реализовал.
var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

// Вход и регистрация, аккаунт, сессии (план I-1, Task 2; спека identity §6.3) — реализует этап 4 спеки.

// CheckNickname — GET /v1/nicknames/availability: доступность ника.
func (Handler) CheckNickname(context.Context, oapi.CheckNicknameRequestObject) (oapi.CheckNicknameResponseObject, error) {
	return nil, errNotImplemented
}

// StartSignup — POST /v1/auth/signup: начать регистрацию по почте.
func (Handler) StartSignup(context.Context, oapi.StartSignupRequestObject) (oapi.StartSignupResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmSignup — POST /v1/auth/signup/confirm: подтвердить регистрацию кодом.
func (Handler) ConfirmSignup(context.Context, oapi.ConfirmSignupRequestObject) (oapi.ConfirmSignupResponseObject, error) {
	return nil, errNotImplemented
}

// ResendAuthCode — POST /v1/auth/codes/{codeId}/resend: отправить код повторно.
func (Handler) ResendAuthCode(context.Context, oapi.ResendAuthCodeRequestObject) (oapi.ResendAuthCodeResponseObject, error) {
	return nil, errNotImplemented
}

// Login — POST /v1/auth/login: вход по почте и паролю.
func (Handler) Login(context.Context, oapi.LoginRequestObject) (oapi.LoginResponseObject, error) {
	return nil, errNotImplemented
}

// CompleteMfa — POST /v1/auth/mfa: второй фактор входа.
func (Handler) CompleteMfa(context.Context, oapi.CompleteMfaRequestObject) (oapi.CompleteMfaResponseObject, error) {
	return nil, errNotImplemented
}

// StartPasswordReset — POST /v1/auth/password-reset: начать сброс пароля.
func (Handler) StartPasswordReset(context.Context, oapi.StartPasswordResetRequestObject) (oapi.StartPasswordResetResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmPasswordReset — POST /v1/auth/password-reset/confirm: новый пароль по коду.
func (Handler) ConfirmPasswordReset(context.Context, oapi.ConfirmPasswordResetRequestObject) (oapi.ConfirmPasswordResetResponseObject, error) {
	return nil, errNotImplemented
}

// RefreshSession — POST /v1/auth/refresh: обновить токены.
func (Handler) RefreshSession(context.Context, oapi.RefreshSessionRequestObject) (oapi.RefreshSessionResponseObject, error) {
	return nil, errNotImplemented
}

// CancelDeletion — POST /v1/auth/deletion/cancel: отменить удаление аккаунта.
func (Handler) CancelDeletion(context.Context, oapi.CancelDeletionRequestObject) (oapi.CancelDeletionResponseObject, error) {
	return nil, errNotImplemented
}

// GetLegalDocuments — GET /v1/legal/documents: текущие версии документов.
func (Handler) GetLegalDocuments(context.Context, oapi.GetLegalDocumentsRequestObject) (oapi.GetLegalDocumentsResponseObject, error) {
	return nil, errNotImplemented
}

// CreateDeviceChallenge — POST /v1/devices/challenges: задача проверки целостности приложения.
func (Handler) CreateDeviceChallenge(context.Context, oapi.CreateDeviceChallengeRequestObject) (oapi.CreateDeviceChallengeResponseObject, error) {
	return nil, errNotImplemented
}

// Logout — POST /v1/auth/logout: выйти.
func (Handler) Logout(context.Context, oapi.LogoutRequestObject) (oapi.LogoutResponseObject, error) {
	return nil, errNotImplemented
}

// GetMe — GET /v1/me: мой аккаунт.
func (Handler) GetMe(context.Context, oapi.GetMeRequestObject) (oapi.GetMeResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateMe — PATCH /v1/me: настройки аккаунта.
func (Handler) UpdateMe(context.Context, oapi.UpdateMeRequestObject) (oapi.UpdateMeResponseObject, error) {
	return nil, errNotImplemented
}

// ChangeNickname — PATCH /v1/me/nickname: сменить ник.
func (Handler) ChangeNickname(context.Context, oapi.ChangeNicknameRequestObject) (oapi.ChangeNicknameResponseObject, error) {
	return nil, errNotImplemented
}

// ListMySessions — GET /v1/me/sessions: «Где вы вошли».
func (Handler) ListMySessions(context.Context, oapi.ListMySessionsRequestObject) (oapi.ListMySessionsResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeMySession — DELETE /v1/me/sessions/{sessionId}: выйти на устройстве.
func (Handler) RevokeMySession(context.Context, oapi.RevokeMySessionRequestObject) (oapi.RevokeMySessionResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeOtherSessions — POST /v1/me/sessions/revoke-others: выйти везде, кроме этого устройства.
func (Handler) RevokeOtherSessions(context.Context, oapi.RevokeOtherSessionsRequestObject) (oapi.RevokeOtherSessionsResponseObject, error) {
	return nil, errNotImplemented
}

// ChangePassword — POST /v1/me/password: сменить или задать пароль.
func (Handler) ChangePassword(context.Context, oapi.ChangePasswordRequestObject) (oapi.ChangePasswordResponseObject, error) {
	return nil, errNotImplemented
}

// StartEmailChange — POST /v1/me/email-change: начать смену почты.
func (Handler) StartEmailChange(context.Context, oapi.StartEmailChangeRequestObject) (oapi.StartEmailChangeResponseObject, error) {
	return nil, errNotImplemented
}

// ConfirmEmailChange — POST /v1/me/email-change/confirm: подтвердить новую почту.
func (Handler) ConfirmEmailChange(context.Context, oapi.ConfirmEmailChangeRequestObject) (oapi.ConfirmEmailChangeResponseObject, error) {
	return nil, errNotImplemented
}

// AcceptDocuments — POST /v1/me/documents: принять документы.
func (Handler) AcceptDocuments(context.Context, oapi.AcceptDocumentsRequestObject) (oapi.AcceptDocumentsResponseObject, error) {
	return nil, errNotImplemented
}

// DeleteAccount — POST /v1/me/delete: удалить аккаунт.
func (Handler) DeleteAccount(context.Context, oapi.DeleteAccountRequestObject) (oapi.DeleteAccountResponseObject, error) {
	return nil, errNotImplemented
}

// 2FA, ключи доступа, соцвходы, способы входа, свежий вход (план I-1, Task 3; спека identity §6.3) — реализуют этапы 5,
// 7 и 8 спеки.

// GetAuthMethods — GET /v1/auth/methods: способы входа.
func (Handler) GetAuthMethods(context.Context, oapi.GetAuthMethodsRequestObject) (oapi.GetAuthMethodsResponseObject, error) {
	return nil, errNotImplemented
}

// StartMfaPasskey — POST /v1/auth/mfa/passkey-options: задача WebAuthn для второго фактора.
func (Handler) StartMfaPasskey(context.Context, oapi.StartMfaPasskeyRequestObject) (oapi.StartMfaPasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// CreateSocialNonce — POST /v1/auth/social/nonces: nonce для входа через Apple или Google.
func (Handler) CreateSocialNonce(context.Context, oapi.CreateSocialNonceRequestObject) (oapi.CreateSocialNonceResponseObject, error) {
	return nil, errNotImplemented
}

// SocialLogin — POST /v1/auth/social: войти через Apple или Google.
func (Handler) SocialLogin(context.Context, oapi.SocialLoginRequestObject) (oapi.SocialLoginResponseObject, error) {
	return nil, errNotImplemented
}

// CompleteSocialSignup — POST /v1/auth/social/signup: дорегистрация после Apple или Google.
func (Handler) CompleteSocialSignup(context.Context, oapi.CompleteSocialSignupRequestObject) (oapi.CompleteSocialSignupResponseObject, error) {
	return nil, errNotImplemented
}

// StartPasskeyLogin — POST /v1/auth/passkey/options: задача WebAuthn для входа ключом.
func (Handler) StartPasskeyLogin(context.Context, oapi.StartPasskeyLoginRequestObject) (oapi.StartPasskeyLoginResponseObject, error) {
	return nil, errNotImplemented
}

// PasskeyLogin — POST /v1/auth/passkey: войти ключом доступа.
func (Handler) PasskeyLogin(context.Context, oapi.PasskeyLoginRequestObject) (oapi.PasskeyLoginResponseObject, error) {
	return nil, errNotImplemented
}

// Reauthenticate — POST /v1/me/reauth: свежий вход.
func (Handler) Reauthenticate(context.Context, oapi.ReauthenticateRequestObject) (oapi.ReauthenticateResponseObject, error) {
	return nil, errNotImplemented
}

// StartReauthPasskey — POST /v1/me/reauth/passkey-options: задача WebAuthn для свежего входа.
func (Handler) StartReauthPasskey(context.Context, oapi.StartReauthPasskeyRequestObject) (oapi.StartReauthPasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// ListMyAuthMethods — GET /v1/me/auth-methods: мои способы входа.
func (Handler) ListMyAuthMethods(context.Context, oapi.ListMyAuthMethodsRequestObject) (oapi.ListMyAuthMethodsResponseObject, error) {
	return nil, errNotImplemented
}

// LinkIdentity — POST /v1/me/identities: привязать Apple или Google.
func (Handler) LinkIdentity(context.Context, oapi.LinkIdentityRequestObject) (oapi.LinkIdentityResponseObject, error) {
	return nil, errNotImplemented
}

// UnlinkIdentity — DELETE /v1/me/identities/{identityId}: отвязать Apple или Google.
func (Handler) UnlinkIdentity(context.Context, oapi.UnlinkIdentityRequestObject) (oapi.UnlinkIdentityResponseObject, error) {
	return nil, errNotImplemented
}

// GetMyMfa — GET /v1/me/mfa: состояние 2FA.
func (Handler) GetMyMfa(context.Context, oapi.GetMyMfaRequestObject) (oapi.GetMyMfaResponseObject, error) {
	return nil, errNotImplemented
}

// SetupTotp — POST /v1/me/mfa/totp/setup: начать включение 2FA.
func (Handler) SetupTotp(context.Context, oapi.SetupTotpRequestObject) (oapi.SetupTotpResponseObject, error) {
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

// RegenerateRecoveryCodes — POST /v1/me/mfa/recovery-codes: новые резервные коды.
func (Handler) RegenerateRecoveryCodes(context.Context, oapi.RegenerateRecoveryCodesRequestObject) (oapi.RegenerateRecoveryCodesResponseObject, error) {
	return nil, errNotImplemented
}

// StartPasskeyRegistration — POST /v1/me/passkeys/registration-options: задача WebAuthn для нового ключа.
func (Handler) StartPasskeyRegistration(context.Context, oapi.StartPasskeyRegistrationRequestObject) (oapi.StartPasskeyRegistrationResponseObject, error) {
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

// DeletePasskey — DELETE /v1/me/passkeys/{passkeyId}: удалить ключ доступа.
func (Handler) DeletePasskey(context.Context, oapi.DeletePasskeyRequestObject) (oapi.DeletePasskeyResponseObject, error) {
	return nil, errNotImplemented
}

// CancelMyMfaReset — DELETE /v1/me/mfa-reset: отменить сброс 2FA через поддержку.
func (Handler) CancelMyMfaReset(context.Context, oapi.CancelMyMfaResetRequestObject) (oapi.CancelMyMfaResetResponseObject, error) {
	return nil, errNotImplemented
}

// Капитанство (план I-1, Task 4; спека identity §3.12, §6.3) — реализует этап 9 спеки.

// GetMyCaptaincy — GET /v1/me/captaincy: моё капитанство.
func (Handler) GetMyCaptaincy(context.Context, oapi.GetMyCaptaincyRequestObject) (oapi.GetMyCaptaincyResponseObject, error) {
	return nil, errNotImplemented
}

// ListMyCaptainApplications — GET /v1/me/captain-applications: мои заявки в капитаны.
func (Handler) ListMyCaptainApplications(context.Context, oapi.ListMyCaptainApplicationsRequestObject) (oapi.ListMyCaptainApplicationsResponseObject, error) {
	return nil, errNotImplemented
}

// SubmitCaptainApplication — POST /v1/captain-applications: подать заявку в капитаны.
func (Handler) SubmitCaptainApplication(context.Context, oapi.SubmitCaptainApplicationRequestObject) (oapi.SubmitCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// WithdrawCaptainApplication — POST /v1/captain-applications/{applicationId}/withdraw: отозвать заявку в капитаны.
func (Handler) WithdrawCaptainApplication(context.Context, oapi.WithdrawCaptainApplicationRequestObject) (oapi.WithdrawCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}
