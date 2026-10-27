package approvals_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// t0 — «сейчас» тестов; микросекунды — как в базе.
var t0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

const (
	actGrant  = "test.grant"  // право по payload: grant или grant_elevated (Permit)
	actRename = "test.rename" // scoped-право с городом: geo.cities.rename
)

// payload — аргументы тестового действия: цель, город, «повышенная» роль и что сделать исполнителю.
type payload struct {
	V        int        `json:"v"`
	Target   *uuid.UUID `json:"target,omitempty"`
	City     *uuid.UUID `json:"city,omitempty"`
	Elevated bool       `json:"elevated,omitempty"`
	Note     string     `json:"note,omitempty"` // свободный текст: в аудит не попадает
	// InitVer — версия учётки инициатора на момент запроса (как у staff в 8b); nil — без сверки версии
	InitVer *int `json:"init_ver,omitempty"`
}

// env — сервис под ролью прода admin, часы, реестр с тестовыми действиями и подменяемым
// исполнителем.
type env struct {
	t     *testing.T
	pool  *pgxpool.Pool // роль admin (grants.sql)
	owner *pgxpool.Pool // суперпользователь: проверки
	clock *clocktest.Fake
	reg   *approvals.Registry
	svc   *approvals.Service

	mu    sync.Mutex
	exec  approvals.Executor        // исполнитель обоих действий; nil — успех с записью аудита
	staff map[uuid.UUID]*auth.Staff // учётки для проверки инициатора
	off   map[uuid.UUID]bool        // выключенные инициаторы
	ver   map[uuid.UUID]int         // версия учётки: растёт при выключении, сбросе, смене роли
	execs atomic.Int32              // сколько раз исполнитель вызван
	check approvals.InitiatorCheck  // подмена проверки инициатора; nil — модель staff ниже
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "admin")
	e := &env{t: t, pool: pools.As, owner: pools.Owner, clock: clocktest.New(t0),
		staff: map[uuid.UUID]*auth.Staff{}, off: map[uuid.UUID]bool{}, ver: map[uuid.UUID]int{}}
	e.reg = approvals.NewRegistry()
	e.reg.Register(approvals.Action{
		Name: actGrant, Permission: "staff.roles.grant",
		Permit: func(raw json.RawMessage) (string, error) {
			var p payload
			if err := json.Unmarshal(raw, &p); err != nil {
				return "", err
			}
			if p.Elevated {
				return "staff.roles.grant_elevated", nil
			}
			return "staff.roles.grant", nil
		},
		Exec: e.run, Target: target,
	})
	e.reg.Register(approvals.Action{Name: actRename, Permission: "geo.cities.rename", Exec: e.run, Target: target})
	e.reg.SetInitiatorCheck(e.initiatorCheck)
	e.svc = approvals.New(e.pool, e.clock, e.reg)
	return e
}

func target(raw json.RawMessage) (uuid.UUID, uuid.UUID) {
	var p payload
	_ = json.Unmarshal(raw, &p)
	var tg, city uuid.UUID
	if p.Target != nil {
		tg = *p.Target
	}
	if p.City != nil {
		city = *p.City
	}
	return tg, city
}

// run — исполнитель: подменённый тестом или по умолчанию — запись аудита модуля через ctx.
func (e *env) run(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	e.execs.Add(1)
	e.mu.Lock()
	ex := e.exec
	e.mu.Unlock()
	if ex != nil {
		return ex(ctx, tx, raw, approver)
	}
	return moduleWrite(ctx, approver)
}

// moduleWrite — «изменение модуля» исполнителя: запись аудита и событие в транзакции подтверждения.
func moduleWrite(ctx context.Context, approver *auth.Staff) error {
	if err := audit.Write(ctx, audit.Entry{ActorStaffID: approver.StaffID, ActorRole: approver.Role,
		ActorLabel: "Исполнитель", Action: "test.executed", ObjectType: "test", ObjectID: approver.StaffID}); err != nil {
		return err
	}
	_, err := events.Publish(ctx, events.Event{Type: "test.executed", SchemaVersion: 1, AggregateType: "test",
		AggregateID: approver.StaffID, AggregateVersion: 1, Payload: map[string]any{}})
	return err
}

func (e *env) setExec(ex approvals.Executor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.exec = ex
}

// initiatorCheck — модель проверки staff (8b): учётка активна, право запроса в городе запроса не deny
// и версия учётки не менялась с момента запроса (payload.init_ver; нет — без сверки).
func (e *env) initiatorCheck(ctx context.Context, tx pgx.Tx, a approvals.Approval) error {
	e.mu.Lock()
	st, ok := e.staff[a.RequestedBy]
	off := e.off[a.RequestedBy]
	ver := e.ver[a.RequestedBy]
	check := e.check
	e.mu.Unlock()
	if check != nil {
		return check(ctx, tx, a)
	}
	var p payload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return err
	}
	if !ok || off || st.Decide(a.Permission, a.ScopeCityID) == auth.Deny || (p.InitVer != nil && *p.InitVer != ver) {
		return approvals.Fail(errors.New("staff.initiator_inactive"))
	}
	return nil
}

// version — текущая версия учётки (для payload.init_ver).
func (e *env) version(staffID uuid.UUID) *int {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := e.ver[staffID]
	return &v
}

// reactivate — учётка снова активна после выключения или сброса доступа: версия выросла.
func (e *env) reactivate(staffID uuid.UUID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.off, staffID)
	e.ver[staffID]++
}

// member — активный сотрудник роли с городами (модератор).
func (e *env) member(role perm.Role, cities ...uuid.UUID) *auth.Staff {
	st := &auth.Staff{StaffID: id.New(), SessionID: id.New(), Role: string(role), CityIDs: cities,
		Stage: auth.StageActive, Grants: perm.Grants(role)}
	e.mu.Lock()
	e.staff[st.StaffID] = st
	e.mu.Unlock()
	return st
}

func (e *env) disable(staffID uuid.UUID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.off[staffID] = true
}

// request — запрос в своей транзакции «модуля».
func (e *env) request(by *auth.Staff, action string, p payload) approvals.Approval {
	e.t.Helper()
	ap, err := e.tryRequest(by, action, p)
	if err != nil {
		e.t.Fatalf("Request: %v", err)
	}
	return ap
}

func (e *env) tryRequest(by *auth.Staff, action string, p any) (approvals.Approval, error) {
	var ap approvals.Approval
	err := db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ap, err = e.svc.Request(ctx, tx, by, "Инициатор "+by.Role, metaWeb, action, p)
		return err
	})
	return ap, err
}

// row — строка запроса глазами суперпользователя.
type row struct {
	Status, Label, Error, Comment string
	DecidedBy                     *uuid.UUID
	DecidedAt                     *time.Time
}

func (e *env) row(apID uuid.UUID) row {
	e.t.Helper()
	var r row
	var label, errText, comment *string
	if err := e.owner.QueryRow(context.Background(), `SELECT status::text, decided_by_label, error, decision_comment,
		decided_by, decided_at FROM approvals WHERE id = $1`, apID).Scan(&r.Status, &label, &errText, &comment,
		&r.DecidedBy, &r.DecidedAt); err != nil {
		e.t.Fatal(err)
	}
	r.Label, r.Error, r.Comment = deref(label), deref(errText), deref(comment)
	return r
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// audits — записи аудита запроса с действием action.
func (e *env) audits(apID uuid.UUID, action string) int {
	return e.count(`SELECT count(*) FROM audit_log WHERE object_id = $1 AND action = $2`, apID, action)
}

// decided — события решения запроса.
func (e *env) decided(apID uuid.UUID) int {
	return e.count(`SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2`, approvals.EventDecided, apID)
}

// moduleWrites — изменения «модуля» (аудит и события исполнителя по умолчанию).
func (e *env) moduleWrites() int {
	return e.count(`SELECT count(*) FROM audit_log WHERE action = 'test.executed'`) +
		e.count(`SELECT count(*) FROM outbox WHERE event_type = 'test.executed'`)
}

// isCode — ошибка с кодом клиента.
func isErr(err, want error) bool { return errors.Is(err, want) }

// metaWeb — IP и User-Agent запроса сотрудника (аудит).
var metaWeb = approvals.Meta{IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "Firefox/140 test"}
