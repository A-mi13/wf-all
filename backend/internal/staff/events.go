package staff

import (
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/events"
)

// События модуля staff (спека сотрудников §4.12) — контракт для подписчиков. Агрегат — учётка
// сотрудника, версия — staff_accounts.version после изменения. Payload — только id и роль, без
// почты и имени; схема v1.
const (
	EventInvited     = "staff.invited"
	EventRoleGranted = "staff.role_granted"
	EventRoleRevoked = "staff.role_revoked"
	EventAccessReset = "staff.access_reset"
	EventLocked      = "staff.locked"

	AggregateAccount = "staff_account"
)

// Invited — payload staff.invited: учётка создана приглашением с ролью.
type Invited struct {
	StaffID uuid.UUID `json:"staff_id"`
	Role    string    `json:"role"`
}

// RoleGranted — payload staff.role_granted: новая роль и, у модератора, города целиком.
type RoleGranted struct {
	StaffID uuid.UUID   `json:"staff_id"`
	Role    string      `json:"role"`
	CityIDs []uuid.UUID `json:"city_ids,omitempty"`
}

// RoleRevoked — payload staff.role_revoked: выключение учётки (Role — снятая роль) или снятие
// части городов модератора (RemovedCityIDs, роль та же; решение контроллера 06.10, п. 7).
type RoleRevoked struct {
	StaffID        uuid.UUID   `json:"staff_id"`
	Role           string      `json:"role,omitempty"`
	RemovedCityIDs []uuid.UUID `json:"removed_city_ids,omitempty"`
}

// AccessReset — payload staff.access_reset: пароль и второй фактор сброшены, выпущен новый код.
type AccessReset struct {
	StaffID uuid.UUID `json:"staff_id"`
}

// Locked — payload staff.locked: пять неудач второго фактора подряд, вход закрыт до LockedUntil.
type Locked struct {
	StaffID     uuid.UUID `json:"staff_id"`
	LockedUntil time.Time `json:"locked_until"`
}

func accountEvent(typ string, staffID uuid.UUID, version int64, p any) events.Event {
	return events.Event{
		Type: typ, SchemaVersion: 1,
		AggregateType: AggregateAccount, AggregateID: staffID, AggregateVersion: version,
		Payload: p,
	}
}

// InvitedEvent — событие приглашения; version — версия учётки после изменения.
func InvitedEvent(staffID uuid.UUID, version int64, role string) events.Event {
	return accountEvent(EventInvited, staffID, version, Invited{StaffID: staffID, Role: role})
}

// RoleGrantedEvent — новая роль; cities — города модератора (у глобальных ролей — nil).
func RoleGrantedEvent(staffID uuid.UUID, version int64, role string, cities []uuid.UUID) events.Event {
	return accountEvent(EventRoleGranted, staffID, version, RoleGranted{StaffID: staffID, Role: role, CityIDs: cities})
}

// RoleRevokedEvent — учётка выключена, роль role снята.
func RoleRevokedEvent(staffID uuid.UUID, version int64, role string) events.Event {
	return accountEvent(EventRoleRevoked, staffID, version, RoleRevoked{StaffID: staffID, Role: role})
}

// CitiesRevokedEvent — у модератора сняты города removed (роль осталась).
func CitiesRevokedEvent(staffID uuid.UUID, version int64, removed []uuid.UUID) events.Event {
	return accountEvent(EventRoleRevoked, staffID, version, RoleRevoked{StaffID: staffID, RemovedCityIDs: removed})
}

// AccessResetEvent — доступ сброшен (пароль и второй фактор).
func AccessResetEvent(staffID uuid.UUID, version int64) events.Event {
	return accountEvent(EventAccessReset, staffID, version, AccessReset{StaffID: staffID})
}

// LockedEvent — учётка закрыта до lockedUntil после пяти неудач второго фактора подряд.
func LockedEvent(staffID uuid.UUID, version int64, lockedUntil time.Time) events.Event {
	return accountEvent(EventLocked, staffID, version, Locked{StaffID: staffID, LockedUntil: lockedUntil.UTC()})
}
