package app_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
)

// menv — сценарии управления (8b): env с подтверждениями вдвоём, собранными как в admin-api (10c):
// реестр, сервис на пуле роли admin, RegisterApprovals, Validate.
type menv struct {
	*env
	reg *approvals.Registry
	svc *approvals.Service
}

func newManageEnv(t *testing.T) *menv {
	t.Helper()
	e := newEnv(t)
	reg := approvals.NewRegistry()
	svc := approvals.New(e.deps.Pool, e.clock, reg)
	e.app.RegisterApprovals(reg, svc)
	if err := reg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return &menv{env: e, reg: reg, svc: svc}
}

// as — сотрудник m с активной сессией, как его загрузил бы конвейер (права роли — perm.Grants).
func (e *menv) as(m member) *auth.Staff {
	e.t.Helper()
	tok, _ := e.rawSession(m.ID, auth.StageActive, ipHome, metaWeb.UserAgent, e.clock.Now())
	return e.load(tok)
}

var apMeta = approvals.Meta{IP: ipHome, UserAgent: metaWeb.UserAgent}

// approve — подтверждение запроса сотрудником st.
func (e *menv) approve(st *auth.Staff, apID uuid.UUID) (approvals.Approval, error) {
	return e.svc.Approve(context.Background(), st, "Подтверждающий", apMeta, apID, "")
}

// mustApprove — подтверждение, которое обязано исполниться.
func (e *menv) mustApprove(st *auth.Staff, apID uuid.UUID) {
	e.t.Helper()
	ap, err := e.approve(st, apID)
	if err != nil || ap.Status != approvals.StatusExecuted {
		e.t.Fatalf("approve: %+v, %v", ap, err)
	}
}

// acctRow — учётка глазами суперпользователя.
type acctRow struct {
	Status, Role, Name string
	Version            int64
	HasPassword        bool
	CreatedBy          *uuid.UUID
}

func (e *menv) acct(id uuid.UUID) acctRow {
	e.t.Helper()
	var r acctRow
	if err := e.owner.QueryRow(context.Background(), `SELECT a.status::text, r.role::text, a.display_name, a.version,
		a.password_hash IS NOT NULL, a.created_by FROM staff_accounts a JOIN staff_roles r ON r.staff_id = a.id
		WHERE a.id = $1`, id).Scan(&r.Status, &r.Role, &r.Name, &r.Version, &r.HasPassword, &r.CreatedBy); err != nil {
		e.t.Fatalf("учётка %s: %v", id, err)
	}
	return r
}

// citiesOf — города сотрудника по возрастанию.
func (e *menv) citiesOf(id uuid.UUID) []uuid.UUID {
	e.t.Helper()
	rows, err := e.owner.Query(context.Background(), `SELECT city_id FROM staff_role_cities WHERE staff_id = $1 ORDER BY city_id`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var c uuid.UUID
		if err := rows.Scan(&c); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// apState — статус и error запроса.
func (e *menv) apState(apID uuid.UUID) (status, errText string) {
	e.t.Helper()
	var et *string
	if err := e.owner.QueryRow(context.Background(), `SELECT status::text, error FROM approvals WHERE id = $1`, apID).
		Scan(&status, &et); err != nil {
		e.t.Fatal(err)
	}
	if et != nil {
		errText = *et
	}
	return status, errText
}

// events — события outbox типа typ агрегата agg.
func (e *menv) events(typ string, agg uuid.UUID) int {
	return e.count(`SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2`, typ, agg)
}

// holdApproval — отдельная транзакция держит замок строки запроса до release (как откатывающийся
// approve): CancelByInitiator её пропускает (SKIP LOCKED).
func (e *menv) holdApproval(apID uuid.UUID) (release func()) {
	e.t.Helper()
	tx, err := e.owner.Begin(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `SELECT 1 FROM approvals WHERE id = $1 FOR UPDATE`, apID); err != nil {
		e.t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = tx.Rollback(context.Background()) }) }
	e.t.Cleanup(release)
	return release
}

// wantForbidden — 403 auth.forbidden.
func wantForbidden(t *testing.T, err error) {
	t.Helper()
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !ok || pe.Problem().Code != httpx.CodeForbidden {
		t.Fatalf("ждали auth.forbidden, получили %v", err)
	}
}

// sortedIDs — копия по возрастанию.
func sortedIDs(ids ...uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return out
}

// barrier — n горутин стартуют вместе: каждая ждёт общий сигнал.
func barrier(n int, fn func(i int)) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := range n {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			fn(i)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}
