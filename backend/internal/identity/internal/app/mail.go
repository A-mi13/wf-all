package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/mail"
)

// Виды писем I-2 (спека §8.2, Приложение 2 плана): ref — строка, по которой Composer (Task 19) находит адрес и
// язык. Письма I-3/I-4 (new_device, identity_*, passkey_*, captain_*) — их планы.
const (
	MailSignupCode               = "identity.signup_code"                // ref — auth_codes.id
	MailSignupAccountExists      = "identity.signup_account_exists"      // ref — auth_codes.id (приманка)
	MailPasswordResetCode        = "identity.password_reset_code"        // ref — auth_codes.id
	MailEmailChangeCode          = "identity.email_change_code"          // ref — auth_codes.id
	MailEmailChangeTaken         = "identity.email_change_taken"         // ref — auth_codes.id (приманка)
	MailEmailChanged             = "identity.email_changed"              // ref — security_events.id (старая почта)
	MailPasswordChanged          = "identity.password_changed"           // ref — security_events.id
	MailSessionRevokedSuspicious = "identity.session_revoked_suspicious" // ref — security_events.id
	MailMfaEnabled               = "identity.mfa_enabled"                // ref — security_events.id
	MailMfaDisabled              = "identity.mfa_disabled"               // ref — security_events.id
	MailRecoveryCodeUsed         = "identity.recovery_code_used"         // ref — security_events.id
	MailRecoveryCodesRegenerated = "identity.recovery_codes_regenerated" // ref — security_events.id
	MailMfaFailures              = "identity.mfa_failures"               // ref — security_events.id
	MailAccountDeletionScheduled = "identity.account_deletion_scheduled" // ref — security_events.id
	MailAccountRestored          = "identity.account_restored"           // ref — security_events.id
	MailAccountDeleted           = "identity.account_deleted"            // ref — security_events.id (адрес — meta_enc)
	MailMfaResetScheduled        = "identity.mfa_reset_scheduled"        // ref — mfa_resets.id
	MailMfaResetCancelled        = "identity.mfa_reset_cancelled"        // ref — mfa_resets.id
	MailMfaResetDone             = "identity.mfa_reset_done"             // ref — mfa_resets.id
)

// MailKinds — все виды писем I-2 (сверка с Composer воркера — Task 19).
func MailKinds() []string {
	return []string{MailSignupCode, MailSignupAccountExists, MailPasswordResetCode, MailEmailChangeCode,
		MailEmailChangeTaken, MailEmailChanged, MailPasswordChanged, MailSessionRevokedSuspicious, MailMfaEnabled,
		MailMfaDisabled, MailRecoveryCodeUsed, MailRecoveryCodesRegenerated, MailMfaFailures,
		MailAccountDeletionScheduled, MailAccountRestored, MailAccountDeleted, MailMfaResetScheduled,
		MailMfaResetCancelled, MailMfaResetDone}
}

// enqueueMail — задача письма в tx действия (очередь mail, исполняет воркер): в аргументах только вид и ref, адреса
// нет — его находит Composer.
func (a *App) enqueueMail(ctx context.Context, tx pgx.Tx, kind string, ref uuid.UUID) error {
	if _, err := a.river.InsertTx(ctx, tx, mail.SendArgs{V: 1, Mail: kind, Ref: ref}, nil); err != nil {
		return fmt.Errorf("identity: письмо %s: %w", kind, err)
	}
	return nil
}
