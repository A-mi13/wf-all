// Package approvals — подтверждение вдвоём (спека сотрудников §4.8, D2, D17, P13): модуль
// регистрирует действие и исполнителя, платформа хранит запрос в approvals, решает, кто вправе его
// подтвердить, и исполняет его в транзакции подтверждения под savepoint. Аудит каждого шага и
// событие platform.approval_decided пишутся в той же транзакции, что и решение.
//
// Правило двух рук: решает не инициатор и не цель (ErrSelf, CHECK approvals_two_hands); право
// подтверждающего — сохранённое право запроса (approvals.permission, вычислено Action.Permit при
// Request), а не пересчёт из текущего payload; инициатор на момент подтверждения ещё активен и
// имеет право запроса (InitiatorCheck, регистрирует staff).
package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

const (
	// TTL — сколько запрос ждёт решения (спека §4.8).
	TTL = 24 * time.Hour
	// DecidedRetention — сколько хранятся решённые запросы (P27: PurgeDecided из тикера чистки 10c).
	DecidedRetention = 180 * 24 * time.Hour

	StatusPending   = "pending"
	StatusExecuted  = "executed"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
	StatusFailed    = "failed"

	// BoxIncoming — запросы, которые сотрудник может решить; BoxMine — созданные им.
	BoxIncoming = "incoming"
	BoxMine     = "mine"

	// EventDecided — событие решения запроса: агрегат approval, версия 1 (запрос решается один раз).
	EventDecided = "platform.approval_decided"
	// AggregateApproval — тип агрегата события и объекта аудита.
	AggregateApproval = "approval"

	// ErrorInitiatorChanged — причина у погашенного системой (CancelByInitiator) и у failed по
	// InitiatorCheck: инициатор выключен, сброшен или лишился права запроса.
	ErrorInitiatorChanged = "initiator_changed"
	// ErrorExecutionFailed — причина failed, если отказ исполнителя без кода клиента (ProblemError или
	// Code()): свободный текст ошибки в строку и аудит не пишется — в нём могут быть ПД.
	ErrorExecutionFailed = "execution_failed"
	// SystemLabel — метка решившего у просрочки и гашения системой (CHECK approvals_system_decider).
	SystemLabel = "system"
)

// statuses — все состояния запроса (approval_status из 0020, ApprovalStatus контракта).
var statuses = []string{StatusPending, StatusExecuted, StatusRejected, StatusCancelled, StatusExpired, StatusFailed}

// Ошибки решений — ответы клиенту (спека §7). Сравнивать — errors.Is.
var (
	ErrNotFound  error = httpx.NewError(http.StatusNotFound, httpx.CodeApprovalNotFound)
	ErrSelf      error = httpx.NewError(http.StatusConflict, httpx.CodeApprovalSelf)
	ErrStale     error = httpx.NewError(http.StatusConflict, httpx.CodeApprovalStale)
	ErrClosed    error = httpx.NewError(http.StatusConflict, httpx.CodeApprovalClosed)
	ErrForbidden error = httpx.NewError(http.StatusForbidden, httpx.CodeForbidden)
)

// errNoInitiatorCheck — подтверждение без проверки инициатора не исполняется (fail-closed): сборка
// admin-api (10c) обязана зарегистрировать проверку (staff) до старта сервера — Registry.Validate.
var errNoInitiatorCheck = errors.New("approvals: проверка инициатора не зарегистрирована (Registry.SetInitiatorCheck)")

// failure — бизнес-отказ исполнителя или проверки инициатора (Fail).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// Fail — бизнес-отказ исполнителя или проверки инициатора: изменения исполнителя откатываются
// (savepoint), запрос становится failed с текстом ошибки (код ошибки модуля), наружу уходит err как
// есть (errors.Is(…, staff.ErrLastOwner)). Ошибка без Fail (кроме ErrStale) — сбой: откатывается вся
// транзакция подтверждения, запрос остаётся pending. Fail(nil) — nil.
func Fail(err error) error {
	if err == nil {
		return nil
	}
	return &failure{err: err}
}

// business — ошибка, которую увидит клиент, если err — бизнес-отказ (Fail или ErrStale); nil — сбой.
func business(err error) error {
	if f, ok := errors.AsType[*failure](err); ok {
		return f.err
	}
	if errors.Is(err, ErrStale) {
		return ErrStale
	}
	return nil
}

// Executor — исполнитель действия в транзакции подтверждения (под savepoint): пишет только таблицы
// своего модуля, аудит и события — через ctx (db.InTx). approver — подтверждающий.
type Executor func(ctx context.Context, tx pgx.Tx, payload json.RawMessage, approver *auth.Staff) error

// Action — действие «вдвоём», которое модуль регистрирует при сборке.
type Action struct {
	Name       string // <модуль>.<действие>: staff.invite
	Permission string // право запроса, если Permit == nil
	// Permit — право запроса по payload (staff.invite: grant или grant_elevated по роли). Вычисляется
	// при Request и хранится в строке: подтверждающий сверяется с ним, а не с пересчётом.
	Permit func(payload json.RawMessage) (string, error)
	Exec   Executor
	// Target — цель (сотрудник; uuid.Nil — цели ещё нет) и город запроса (uuid.Nil — без города).
	// nil — ни цели, ни города.
	Target func(payload json.RawMessage) (targetID, cityID uuid.UUID)
}

// InitiatorCheck — проверка инициатора a.RequestedBy в транзакции подтверждения (регистрирует staff).
// Получает запрос целиком: право (a.Permission), город (a.ScopeCityID — для scoped-действий), payload и
// время запроса (a.RequestedAt). КОНТРАКТ: проверка обязана отказать, если инициатор сейчас не активен
// или без права запроса, И если с момента requested_at он выключался, сбрасывался или менял роль — даже
// если сейчас снова активен с тем же правом (staff кладёт версию учётки инициатора в payload при Request
// и сравнивает с текущей). На это полагается CancelByInitiator: строку под замком параллельного решения
// он пропускает (SKIP LOCKED), и если то решение откатится, запрос останется pending — отсечь его при
// следующем подтверждении может только эта проверка. Отказ — Fail(…) или ErrStale: запрос становится
// failed (initiator_changed), наружу ErrStale. Иная ошибка — сбой (откат, запрос pending).
type InitiatorCheck func(ctx context.Context, tx pgx.Tx, a Approval) error

// Registry — действия «вдвоём» и проверка инициатора. Заполняется при сборке admin-api.
type Registry struct {
	mu      sync.RWMutex
	actions map[string]Action
	check   InitiatorCheck
}

// NewRegistry — пустой реестр.
func NewRegistry() *Registry {
	return &Registry{actions: map[string]Action{}}
}

// actionName — формат имени действия (CHECK approvals_action_format).
var actionName = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Register — действие модуля. Ошибка сборки — паника: имя не по формату, нет исполнителя, право не
// из каталога perm (или черновое), дубль имени.
func (r *Registry) Register(a Action) {
	if !actionName.MatchString(a.Name) {
		panic(fmt.Sprintf("approvals: имя действия %q не по формату <модуль>.<действие>", a.Name))
	}
	if a.Exec == nil {
		panic("approvals: действие " + a.Name + " без исполнителя")
	}
	if err := checkPermission(a.Permission); err != nil {
		panic("approvals: действие " + a.Name + ": " + err.Error())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.actions[a.Name]; dup {
		panic("approvals: действие " + a.Name + " уже зарегистрировано")
	}
	r.actions[a.Name] = a
}

// SetInitiatorCheck — проверка инициатора (регистрирует staff). Второй вызов или nil — паника.
func (r *Registry) SetInitiatorCheck(c InitiatorCheck) {
	if c == nil {
		panic("approvals: пустая проверка инициатора")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.check != nil {
		panic("approvals: проверка инициатора уже зарегистрирована")
	}
	r.check = c
}

// Actions — имена действий по алфавиту (страж подписей approvals.actions.<действие>, D21).
func (r *Registry) Actions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.actions))
	for n := range r.actions {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Validate — реестр готов к подтверждениям: есть действия — есть и проверка инициатора. Сборка
// admin-api (10c) зовёт после регистрации модулей, до старта сервера.
func (r *Registry) Validate() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.actions) > 0 && r.check == nil {
		return errNoInitiatorCheck
	}
	return nil
}

func (r *Registry) action(name string) (Action, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[name]
	return a, ok
}

func (r *Registry) initiatorCheck() InitiatorCheck {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.check
}

// checkPermission — право из каталога perm, не черновое.
func checkPermission(key string) error {
	p, ok := perm.Lookup(key)
	switch {
	case !ok:
		return fmt.Errorf("право %q не из каталога perm", key)
	case p.Draft:
		return fmt.Errorf("право %q черновое — его нет ни у одной роли", key)
	}
	return nil
}

// Approval — запрос на подтверждение. Нулевые значения — NULL в базе: TargetID, ScopeCityID,
// DecidedBy — uuid.Nil; DecidedByLabel, Comment, Error — "".
type Approval struct {
	ID                 uuid.UUID
	Action, Permission string
	Status             string
	Payload            json.RawMessage
	TargetID           uuid.UUID
	ScopeCityID        uuid.UUID
	RequestedBy        uuid.UUID
	RequestedByLabel   string
	RequestedAt        time.Time
	ExpiresAt          time.Time
	DecidedBy          uuid.UUID
	DecidedByLabel     string
	DecidedAt          *time.Time
	Comment, Error     string
}

// Meta — откуда пришёл запрос сотрудника: IP и User-Agent для аудита (спека §4.12). Нулевые
// значения — NULL в журнале. Модуль передаёт свой staff.Meta, ручки 10b — из запроса.
type Meta struct {
	IP        netip.Addr
	UserAgent string
}
