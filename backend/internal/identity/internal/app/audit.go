package app

import (
	"context"

	"github.com/google/uuid"

	"wf/backend/internal/platform/audit"
)

// Действия аудита I-2 (спека §8.3, Приложение 2 плана): запись — задача сценария, в транзакции действия.
const (
	ActionUserRegistered        = "identity.user_registered"
	ActionLoginFailed           = "identity.login_failed" // anonymous, без object_id (журнал — не оракул)
	ActionPasswordChanged       = "identity.password_changed"
	ActionPasswordReset         = "identity.password_reset"
	ActionEmailChanged          = "identity.email_changed"
	ActionNicknameChanged       = "identity.nickname_changed"
	ActionDocumentsAccepted     = "identity.documents_accepted"
	ActionMfaEnabled            = "identity.mfa_enabled"
	ActionMfaDisabled           = "identity.mfa_disabled"
	ActionSessionsRevoked       = "identity.sessions_revoked"
	ActionUserDeletionRequested = "identity.user_deletion_requested"
	ActionUserDeletionCancelled = "identity.user_deletion_cancelled"
	ActionUserDeleted           = "identity.user_deleted" // system
	ActionMfaResetRequested     = "identity.mfa_reset_requested"
	ActionMfaResetCancelled     = "identity.mfa_reset_cancelled"
	ActionMfaResetExecuted      = "identity.mfa_reset_executed" // system
)

// Действия аудита I-4 (план I-4, Task 2; спека §8.3). ПД (почта, причина доступа) — не в before/after: причина
// доступа — audit.Entry.Reason (R10).
const (
	ActionCaptainApplicationSubmitted = "identity.captain_application_submitted"
	ActionCaptainApplicationWithdrawn = "identity.captain_application_withdrawn"
	ActionCaptainApplicationApproved  = "identity.captain_application_approved"
	ActionCaptainApplicationRejected  = "identity.captain_application_rejected"
	ActionCaptainPermitRevoked        = "identity.captain_permit_revoked"
	ActionPiiRead                     = "identity.pii_read"      // контакты заявителя, найденный игрок (audit.WriteRead)
	ActionPlayerLookup                = "identity.player_lookup" // поиск без результата, без object_id (R10)
	ActionBanExpired                  = "identity.ban_expired"   // system в чистке (R12)
	ActionCityChanged                 = "identity.city_changed"
)

// Автор и объект записей identity.
const (
	rolePlayer    = "player"
	roleAnonymous = "anonymous"
	roleSystem    = "system"
	objectUser    = "user"
)

// ObjectCaptainApplication — тип объекта записей заявок в капитаны (I-4).
const ObjectCaptainApplication = "captain_application"

// auditUser — действие игрока над своим аккаунтом (автор и объект — userID). before/after — без ПД: audit.Mask
// маскирует по словам ключа, свободный текст не класть.
func (a *App) auditUser(ctx context.Context, action string, userID uuid.UUID, before, after map[string]any) error {
	e := audit.Entry{ActorUserID: userID, ActorRole: rolePlayer, Action: action, ObjectType: objectUser,
		ObjectID: userID}
	if before != nil {
		e.Before = before
	}
	if after != nil {
		e.After = after
	}
	return audit.Write(ctx, e)
}

// auditAnonymous — действие без автора и без object_id (неудачный вход существующего аккаунта: журнал не должен
// стать оракулом существования); тип объекта — user (платформа требует тип).
func (a *App) auditAnonymous(ctx context.Context, action string) error {
	return audit.Write(ctx, audit.Entry{ActorRole: roleAnonymous, Action: action, ObjectType: objectUser})
}
