package auth

import (
	"context"
	"errors"
	"net/netip"
	"slices"

	"github.com/google/uuid"
)

// Стадии сессии сотрудника (staff_session_stage, спека §4.5–4.7).
const (
	StageMFAPending = "mfa_pending" // пароль верен, ждём второй фактор (5 мин)
	StageSetup      = "setup"       // первый вход по приглашению: настройка 2FA
	StageActive     = "active"
)

// RoleModerator — единственная роль сотрудника со скоупом городов (спека §2.3); остальные роли
// глобальные. Строкой, а не perm.Role: perm импортирует auth, обратного импорта быть не может.
const RoleModerator = "moderator"

// Staff — сотрудник запроса (admin-api). Загружается модулем staff по токену cookie
// (StaffSessions) при каждом запросе и дальше не меняется: Grants (права роли из perm.Grants) и
// CityIDs только читать. Права считаются при загрузке, поэтому модули проверяют их через
// Decide/Can, не импортируя staff (решение R3).
type Staff struct {
	StaffID, SessionID uuid.UUID
	Role               string      // owner|admin|moderator|support
	CityIDs            []uuid.UUID // только у moderator
	Stage              string      // mfa_pending|setup|active
	// Reauth — бездействие > 20 мин или смена IP: сессия жива, но доступны только операции
	// x-permission=session (разблокировка, me, выход).
	Reauth bool
	Grants map[string]Grant
}

// Grant — значение права для роли сотрудника; нет ключа — GrantDeny. От стадии сессии не
// зависит: по нему конвейер admin-api отличает 403 (права нет) от 401 (сессия не дошла до active).
func (s *Staff) Grant(perm string) Grant {
	if g, ok := s.Grants[perm]; ok {
		return g
	}
	return GrantDeny
}

// InScope — город в скоупе сотрудника: глобальные роли — любой город, moderator — только из
// CityIDs (uuid.Nil в скоупе модератора не бывает).
func (s *Staff) InScope(cityID uuid.UUID) bool {
	if s.Role != RoleModerator {
		return true
	}
	return cityID != uuid.Nil && slices.Contains(s.CityIDs, cityID)
}

// Decision — что делать с действием сотрудника (решение P6).
type Decision uint8

const (
	Deny          Decision = iota // нельзя: 403 auth.forbidden
	Allow                         // исполнить сразу
	NeedsApproval                 // только запросом подтверждения вдвоём (202 {approval})
)

// Decide — решение по действию с правом perm над объектом города cityID (uuid.Nil — объект без
// города): Allow → Allow; Scoped → Allow для города в скоупе, иначе (и без города) Deny;
// Dual → NeedsApproval; Deny и нет ключа → Deny. Сессия не в стадии active или под Reauth —
// Deny для любого права: модуль, решающий изнутри сценария, не пустит недовошедшего, даже если
// конвейер admin-api (x-permission) ошибся.
func (s *Staff) Decide(perm string, cityID uuid.UUID) Decision {
	if s.Stage != StageActive || s.Reauth {
		return Deny
	}
	switch s.Grant(perm) {
	case GrantAllow:
		return Allow
	case GrantScoped:
		if cityID != uuid.Nil && s.InScope(cityID) {
			return Allow
		}
		return Deny
	case GrantDual:
		return NeedsApproval
	default:
		return Deny
	}
}

// Can — можно ли исполнить сразу: Decide == Allow. Для Dual — false: такое действие исполняется
// только через подтверждение вдвоём (Decide → NeedsApproval).
func (s *Staff) Can(perm string, cityID uuid.UUID) bool {
	return s.Decide(perm, cityID) == Allow
}

type staffKey struct{}

// WithStaff — ctx с сотрудником запроса.
func WithStaff(ctx context.Context, s *Staff) context.Context {
	return context.WithValue(ctx, staffKey{}, s)
}

// StaffFrom — сотрудник запроса; false — запрос без сессии сотрудника.
func StaffFrom(ctx context.Context) (*Staff, bool) {
	s, ok := ctx.Value(staffKey{}).(*Staff)
	return s, ok && s != nil
}

// AuthenticatedStaff — для httpx.ValidateOptions в admin-api: операции с security контракта
// требуют сессию сотрудника (любой стадии — стадии проверяет x-permission).
func AuthenticatedStaff(ctx context.Context) bool {
	_, ok := StaffFrom(ctx)
	return ok
}

// ErrSessionReplaced — токен сотрудника недавно заменён новым (второй фактор, unlock,
// recovery-codes/ack): запрос со старым токеном ушёл параллельно и вернулся позже. Клиенту — тот же
// 401 auth.unauthenticated, но это не ErrSessionInvalid: конвейер admin-api не стирает cookie (в
// браузере уже новая), не считает отказ в IP-лимите staff_auth и не кладёт токен в негативный кэш.
// Льготу (сколько старый токен считается «заменённым», а не невалидным) держит загрузчик staff.
var ErrSessionReplaced = errors.New("auth: токен сессии сотрудника заменён новым")

// StaffSessions — загрузчик сессии сотрудника (реализует модуль staff: таблица staff_sessions —
// его). Токен — значение cookie как есть, ip — адрес клиента из peer (смена подсети → Reauth),
// background — запрос с X-WF-Background: 1 (опрос счётчиков): не продлевает бездействие
// (решение D10). ErrSessionInvalid — сессии нет, она отозвана или истекла: 401
// auth.unauthenticated; ErrSessionReplaced — токен только что заменён новым (401 без стирания
// cookie); другая ошибка — сбой (500).
type StaffSessions interface {
	LoadStaff(ctx context.Context, token string, ip netip.Addr, background bool) (*Staff, error)
}

// StaffSessionsFunc — функция как StaffSessions (тесты конвейера, заглушки).
type StaffSessionsFunc func(ctx context.Context, token string, ip netip.Addr, background bool) (*Staff, error)

// LoadStaff — вызов функции.
func (f StaffSessionsFunc) LoadStaff(ctx context.Context, token string, ip netip.Addr, background bool) (*Staff, error) {
	return f(ctx, token, ip, background)
}

// NoStaffSessions — до модуля staff (сборка cmd/admin-api, Task 10c): сессий сотрудников нет,
// любой токен — ErrSessionInvalid.
var NoStaffSessions StaffSessions = StaffSessionsFunc(func(context.Context, string, netip.Addr, bool) (*Staff, error) {
	return nil, ErrSessionInvalid
})
