package identity

import (
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/events"
)

// События модуля identity (спека identity §8.1), часть I-2 — контракт для подписчиков будущих модулей. Payload —
// только идентификаторы и непрофильные значения, схема v1 (SchemaVersion); почты, телефона, даты рождения, ника и IP
// нет (TestEventPayloadsNoPII). События I-3/I-4 (identity_linked, passkey_*, captain_*, device_shared, city_changed) —
// их планы.
const (
	EventUserRegistered        = "identity.user_registered"
	EventUserDeletionScheduled = "identity.user_deletion_scheduled"
	EventUserDeletionCancelled = "identity.user_deletion_cancelled"
	EventUserDeleted           = "identity.user_deleted"
	EventUserEmailChanged      = "identity.user_email_changed"
	EventNicknameChanged       = "identity.nickname_changed"
	EventPasswordChanged       = "identity.password_changed"
	EventMfaChanged            = "identity.mfa_changed"
	EventSessionsRevoked       = "identity.sessions_revoked"
	EventRestrictionsChanged   = "identity.restrictions_changed"
	EventMfaResetScheduled     = "identity.mfa_reset_scheduled"
	EventMfaResetCancelled     = "identity.mfa_reset_cancelled"
	AggregateUser              = "user"
	AggregateMfaReset          = "mfa_reset"
	maxSessionIDsInEvent       = 100 // спека §8.1: session_ids ≤ 100
	eventSchemaVersion         = 1
)

// EventTypes — все события I-2 (сверка каталога в тестах).
func EventTypes() []string {
	return []string{EventUserRegistered, EventUserDeletionScheduled, EventUserDeletionCancelled, EventUserDeleted,
		EventUserEmailChanged, EventNicknameChanged, EventPasswordChanged, EventMfaChanged, EventSessionsRevoked,
		EventRestrictionsChanged, EventMfaResetScheduled, EventMfaResetCancelled}
}

// UserRef — агрегат user: id и users.version после изменения (растёт на каждое изменение аккаунта в той же tx).
type UserRef struct {
	ID      uuid.UUID
	Version int64
}

// MfaResetRef — агрегат mfa_reset: mfa_resets.id и версия 1 (назначен) или 2 (отменён); не user — исполнитель в
// admin-api users не пишет (К2).
type MfaResetRef struct {
	ID      uuid.UUID
	Version int64
}

// UserPayload — payload событий, где кроме аккаунта ничего нет.
type UserPayload struct {
	UserID uuid.UUID `json:"user_id"`
}

// UserRegisteredPayload — identity.user_registered: method — password | apple | google, platform — ios | android | web.
type UserRegisteredPayload struct {
	UserID   uuid.UUID `json:"user_id"`
	CityID   uuid.UUID `json:"city_id"`
	Method   string    `json:"method"`
	Platform string    `json:"platform"`
}

// UserDeletionScheduledPayload — identity.user_deletion_scheduled: анонимизация не раньше PurgeAt.
type UserDeletionScheduledPayload struct {
	UserID  uuid.UUID `json:"user_id"`
	PurgeAt time.Time `json:"purge_at"`
}

// MfaChangedPayload — identity.mfa_changed.
type MfaChangedPayload struct {
	UserID  uuid.UUID `json:"user_id"`
	Enabled bool      `json:"enabled"`
}

// SessionsRevokedPayload — identity.sessions_revoked: отозванные сессии (≤ 100) и причина (sessions.revoked_reason).
type SessionsRevokedPayload struct {
	UserID     uuid.UUID   `json:"user_id"`
	SessionIDs []uuid.UUID `json:"session_ids"`
	Reason     string      `json:"reason"`
}

// RestrictionsChangedPayload — identity.restrictions_changed: статус аккаунта и закрытые возможности.
type RestrictionsChangedPayload struct {
	UserID       uuid.UUID `json:"user_id"`
	Status       string    `json:"status"`
	Capabilities []string  `json:"capabilities"`
}

// MfaResetPayload — identity.mfa_reset_scheduled и identity.mfa_reset_cancelled.
type MfaResetPayload struct {
	ResetID   uuid.UUID `json:"reset_id"`
	UserID    uuid.UUID `json:"user_id"`
	ExecuteAt time.Time `json:"execute_at"`
}

func userEvent(typ string, u UserRef, payload any) events.Event {
	return events.Event{Type: typ, SchemaVersion: eventSchemaVersion, AggregateType: AggregateUser,
		AggregateID: u.ID, AggregateVersion: u.Version, Payload: payload}
}

// UserRegistered — аккаунт создан (подтверждение кода регистрации, дорегистрация соцвхода).
func UserRegistered(u UserRef, cityID uuid.UUID, method, platform string) events.Event {
	return userEvent(EventUserRegistered, u,
		UserRegisteredPayload{UserID: u.ID, CityID: cityID, Method: method, Platform: platform})
}

// UserDeletionScheduled — запрос удаления: аккаунт скрыт до анонимизации в purgeAt (не user_deleted).
func UserDeletionScheduled(u UserRef, purgeAt time.Time) events.Event {
	return userEvent(EventUserDeletionScheduled, u, UserDeletionScheduledPayload{UserID: u.ID, PurgeAt: purgeAt.UTC()})
}

// UserDeletionCancelled — удаление отменено входом в период отмены.
func UserDeletionCancelled(u UserRef) events.Event {
	return userEvent(EventUserDeletionCancelled, u, UserPayload{UserID: u.ID})
}

// UserDeleted — необратимая анонимизация (§3.11 п. 3); только здесь.
func UserDeleted(u UserRef) events.Event {
	return userEvent(EventUserDeleted, u, UserPayload{UserID: u.ID})
}

// UserEmailChanged — почта сменена (адресов в payload нет).
func UserEmailChanged(u UserRef) events.Event {
	return userEvent(EventUserEmailChanged, u, UserPayload{UserID: u.ID})
}

// NicknameChanged — ник сменён (самого ника в payload нет: подписчик читает его через API модуля).
func NicknameChanged(u UserRef) events.Event {
	return userEvent(EventNicknameChanged, u, UserPayload{UserID: u.ID})
}

// PasswordChanged — пароль сменён, сброшен или задан.
func PasswordChanged(u UserRef) events.Event {
	return userEvent(EventPasswordChanged, u, UserPayload{UserID: u.ID})
}

// MfaChanged — 2FA включена или выключена (в том числе сбросом поддержкой).
func MfaChanged(u UserRef, enabled bool) events.Event {
	return userEvent(EventMfaChanged, u, MfaChangedPayload{UserID: u.ID, Enabled: enabled})
}

// SessionsRevoked — сессии отозваны; больше 100 id — первые 100 (живых сессий больше 100 не бывает, R17).
func SessionsRevoked(u UserRef, sessionIDs []uuid.UUID, reason string) events.Event {
	ids := make([]uuid.UUID, 0, min(len(sessionIDs), maxSessionIDsInEvent))
	ids = append(ids, sessionIDs[:min(len(sessionIDs), maxSessionIDsInEvent)]...)
	return userEvent(EventSessionsRevoked, u, SessionsRevokedPayload{UserID: u.ID, SessionIDs: ids, Reason: reason})
}

// RestrictionsChanged — статус аккаунта или ограничения изменились (в I-2 — снятие истёкших чисткой).
func RestrictionsChanged(u UserRef, status string, capabilities []string) events.Event {
	caps := append(make([]string, 0, len(capabilities)), capabilities...)
	return userEvent(EventRestrictionsChanged, u,
		RestrictionsChangedPayload{UserID: u.ID, Status: status, Capabilities: caps})
}

func mfaResetEvent(typ string, r MfaResetRef, userID uuid.UUID, executeAt time.Time) events.Event {
	return events.Event{Type: typ, SchemaVersion: eventSchemaVersion, AggregateType: AggregateMfaReset,
		AggregateID: r.ID, AggregateVersion: r.Version,
		Payload: MfaResetPayload{ResetID: r.ID, UserID: userID, ExecuteAt: executeAt.UTC()}}
}

// MfaResetScheduled — сброс 2FA поддержкой назначен (версия агрегата 1).
func MfaResetScheduled(r MfaResetRef, userID uuid.UUID, executeAt time.Time) events.Event {
	return mfaResetEvent(EventMfaResetScheduled, r, userID, executeAt)
}

// MfaResetCancelled — сброс 2FA отменён (версия агрегата 2): владельцем (вход вторым фактором, отмена, снятие 2FA),
// сотрудником, запросом удаления аккаунта или исполнителем по сроку (2FA уже нет, аккаунт удаляется).
func MfaResetCancelled(r MfaResetRef, userID uuid.UUID, executeAt time.Time) events.Event {
	return mfaResetEvent(EventMfaResetCancelled, r, userID, executeAt)
}
