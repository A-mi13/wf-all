// Package identity — игроки: регистрация и вход, 2FA, ключи доступа, соцвходы, сессии, согласия, удаление,
// заявки в капитаны (спека docs/superpowers/specs/2026-11-13-identity-design.md). Корневой пакет — API модуля для
// других модулей; в плане I-1 (контракт до кода) в нём только коды ошибок.
package identity

// Коды ошибок модуля для клиентов: x-error-codes операций identity в контрактах, тексты — errors.<код> в локалях
// фронтов (публичные — apps/web, админские — apps/admin). Каждая константа — хотя бы в одном списке
// (TestCodeConstantsListed). Сверку с контрактами держат:
//   - PublicCodes — TestIdentityCodesDocumented (internal/httpapi/public): коды операций тега identity публичного
//     контракта, в обе стороны;
//   - AdminCodes — TestIdentityAdminCodesDocumented (internal/httpapi/admin): коды identity.* операций контракта
//     admin, в обе стороны;
//   - CrossModuleCodes — TestCrossModuleCodesNotInIdentityOps (internal/httpapi/public): коды модуля, которые
//     отдают операции других модулей; у операций identity их нет (план I-1, R9).
const (
	CodeRestricted = "identity.restricted" // 403: возможность закрыта санкцией (спека identity §6.6; отдают все модули)

	// вход и регистрация, коды из письма, токены, сессии, аккаунт (спека §6.5; план I-1, Task 2)
	CodeInvalidCredentials    = "identity.invalid_credentials"      // 400: почта или пароль неверны (одинаково для «нет аккаунта»)
	CodeAccountBlocked        = "identity.account_blocked"          // 403: аккаунт заблокирован (blocked_until или бессрочно)
	CodeCodeInvalid           = "identity.code_invalid"             // 400: неверный код из письма, есть attempts_left
	CodeCodeExpired           = "identity.code_expired"             // 410: код истёк, погашен или исчерпал попытки
	CodeResendTooSoon         = "identity.resend_too_soon"          // 429: повтор отправки раньше паузы (Retry-After)
	CodeTooYoung              = "identity.too_young"                // 422: возраст ниже порога страны города
	CodeCityNotFound          = "identity.city_not_found"           // 422: города нет или страна выключена
	CodePasswordWeak          = "identity.password_weak"            // 422: пароль короткий, распространённый или длинный (reasons)
	CodeNicknameInvalid       = "identity.nickname_invalid"         // 422: ник не проходит формат §5.8
	CodeNicknameTaken         = "identity.nickname_taken"           // 409: ник занят, в резерве или карантине
	CodeNicknameChangeTooSoon = "identity.nickname_change_too_soon" // 409: смена ника раньше 30 дней (available_at)
	CodeEmailDisposable       = "identity.email_disposable"         // 422: одноразовый почтовый домен
	CodeEmailTaken            = "identity.email_taken"              // 409: почта занята (гонка; виден только владельцу кода)
	CodeEmailSame             = "identity.email_same"               // 422: новая почта совпадает с текущей
	CodeDeviceSignupLimit     = "identity.device_signup_limit"      // 403: с устройства уже 2 регистрации
	CodeDeviceUntrusted       = "identity.device_untrusted"         // 403: аттестация не прошла (режим enforce)
	CodeDeviceRequired        = "identity.device_required"          // 422: мобильный запрос (не от BFF) без device (К5)
	CodeMfaRequired           = "identity.mfa_required"             // 400: сброс пароля при 2FA без второго фактора (methods)
	CodeMfaInvalid            = "identity.mfa_invalid"              // 400: неверный TOTP или резервный код (attempts_left)
	CodeMfaExpired            = "identity.mfa_expired"              // 410: mfa_token истёк или исчерпан
	CodePasskeyInvalid        = "identity.passkey_invalid"          // 400: ключ неизвестен, подпись, origin или rpId не прошли
	CodeRefreshInvalid        = "identity.refresh_invalid"          // 401: refresh неизвестен, истёк, отозван или повторён
	CodeReauthRequired        = "identity.reauth_required"          // 403: нужен свежий вход
	CodeSessionNotFound       = "identity.session_not_found"        // 404: сессии нет или она чужая
	CodeConfirmMismatch       = "identity.confirm_mismatch"         // 422: подтверждение ником не совпало
	CodeDocumentsOutdated     = "identity.documents_outdated"       // 422: нет согласия или версии не текущие
	CodeRestoreTokenInvalid   = "identity.restore_token_invalid"    // 410: токен отмены удаления истёк или использован
)

// PublicCodes — коды операций identity публичного контракта.
var PublicCodes = []string{
	CodeInvalidCredentials, CodeAccountBlocked, CodeCodeInvalid, CodeCodeExpired, CodeResendTooSoon, CodeTooYoung,
	CodeCityNotFound, CodePasswordWeak, CodeNicknameInvalid, CodeNicknameTaken, CodeNicknameChangeTooSoon,
	CodeEmailDisposable, CodeEmailTaken, CodeEmailSame, CodeDeviceSignupLimit, CodeDeviceUntrusted, CodeDeviceRequired,
	CodeMfaRequired, CodeMfaInvalid, CodeMfaExpired, CodePasskeyInvalid, CodeRefreshInvalid, CodeReauthRequired,
	CodeSessionNotFound, CodeConfirmMismatch, CodeDocumentsOutdated, CodeRestoreTokenInvalid,
}

// AdminCodes — коды операций identity контракта admin.
var AdminCodes = []string{}

// CrossModuleCodes — коды identity, которые отдают операции других модулей (спека §6.6, R9).
var CrossModuleCodes = []string{CodeRestricted}
