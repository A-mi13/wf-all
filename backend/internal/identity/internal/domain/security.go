package domain

// Метки HMAC кольца (keys.Ring.MAC) по назначению (спека §5.4, R15).
const (
	LabelCode      = "identity-code"      // код из письма: id ‖ код
	LabelRecovery  = "identity-recovery"  // резервный код: user_id ‖ код
	LabelDevice    = "identity-device"    // устройство: platform ‖ install_id
	LabelRateLimit = "identity-ratelimit" // ключ лимита: purpose ‖ email
)

// SecurityKind — security_events.kind (спека §8.3), часть I-2. Виды I-3 (new_device, identity_*, passkey_*,
// attest_failed) — их план.
type SecurityKind string

const (
	SecSignup                   SecurityKind = "signup"
	SecLogin                    SecurityKind = "login"
	SecLoginFailed              SecurityKind = "login_failed"
	SecRefreshReuse             SecurityKind = "refresh_reuse"
	SecPasswordChanged          SecurityKind = "password_changed"
	SecPasswordReset            SecurityKind = "password_reset"
	SecEmailChanged             SecurityKind = "email_changed"
	SecMfaEnabled               SecurityKind = "mfa_enabled"
	SecMfaDisabled              SecurityKind = "mfa_disabled"
	SecMfaFailures              SecurityKind = "mfa_failures"
	SecRecoveryCodeUsed         SecurityKind = "recovery_code_used"
	SecRecoveryCodesRegenerated SecurityKind = "recovery_codes_regenerated"
	SecAccountDeletionScheduled SecurityKind = "account_deletion_scheduled"
	SecAccountRestored          SecurityKind = "account_restored"
	SecAccountDeleted           SecurityKind = "account_deleted"
	SecMfaResetScheduled        SecurityKind = "mfa_reset_scheduled" // сброс 2FA поддержкой назначен (Task 17)
)

// SecurityKinds — все виды I-2 (для тестов и сверок).
func SecurityKinds() []SecurityKind {
	return []SecurityKind{SecSignup, SecLogin, SecLoginFailed, SecRefreshReuse, SecPasswordChanged, SecPasswordReset,
		SecEmailChanged, SecMfaEnabled, SecMfaDisabled, SecMfaFailures, SecRecoveryCodeUsed, SecRecoveryCodesRegenerated,
		SecAccountDeletionScheduled, SecAccountRestored, SecAccountDeleted, SecMfaResetScheduled}
}
