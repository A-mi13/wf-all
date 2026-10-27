package staff

import (
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
)

// MeView — «кто вошёл» (GET /v1/staff/me, схема StaffMe; R15, P20). В стадии mfa_pending Account —
// nil, CityIDs и Permissions пусты; в setup — учётка без прав.
type MeView struct {
	Account        *MeAccount
	CityIDs        []uuid.UUID
	Stage          string
	ReauthRequired bool
	Permissions    map[string]auth.Grant // права без доступа не перечисляются
	Session        *MeSession            // nil — сессия не определена
}

// MeAccount — поля учётки в MeView.
type MeAccount struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Locale      string
	Role        string
}

// MeSession — текущая сессия: id и абсолютный срок.
type MeSession struct {
	ID        uuid.UUID
	ExpiresAt time.Time
}

// SessionView — сессия сотрудника в «Моём доступе» и карточке (схема StaffSession). IP —
// подсеть /24 или /48 (audit.MaskIP), полный адрес наружу не уходит.
type SessionView struct {
	ID         uuid.UUID
	Stage      string
	Current    bool
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  *string // nil — браузер не прислал User-Agent
}

// AccountView — учётка в списке «Роли и доступ» и карточке (схема StaffAccount).
type AccountView struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Locale      string
	Status      string
	Role        string
	CityIDs     []uuid.UUID
	CreatedAt   time.Time
	LastLoginAt *time.Time
	LockedUntil *time.Time
	Version     int64
	Invite      *InviteState // nil — живого приглашения нет
}

// InviteState — живое приглашение учётки (схема StaffInviteState и служебные поля карточки).
type InviteState struct {
	Purpose   string // invite | access_reset
	ExpiresAt time.Time
	Expired   bool
	IssuedAt  time.Time // выпуск (created_at): от него пауза повторной отправки админом
}

// MFAView — состояние второго фактора (схема StaffMfaStatus).
type MFAView struct {
	ConfirmedAt       *time.Time
	RecoveryCodesLeft int
}

// InviteInput — приглашение нового сотрудника (схема StaffInviteRequest): почта как ввели (её
// нормализует сценарий), имя, роль и города модератора.
type InviteInput struct {
	Email       string
	DisplayName string
	Role        string
	CityIDs     []uuid.UUID
}
