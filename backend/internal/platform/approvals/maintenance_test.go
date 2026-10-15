package approvals_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/perm"
)

// CancelByInitiator — все ожидающие запросы инициатора → cancelled от системы с причиной
// initiator_changed, аудит и событие на каждый; решённые, чужие и запросы о нём не трогаются; в
// транзакции модуля: откат модуля откатывает и гашение.
func TestCancelByInitiator(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	mine := []approvals.Approval{
		e.request(a, actGrant, payload{V: 1}),
		e.request(a, actGrant, payload{V: 1, Target: &b.StaffID}),
		e.request(a, actGrant, payload{V: 1, Elevated: true}),
	}
	done := e.request(a, actGrant, payload{V: 1})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", done.ID, ""); err != nil {
		t.Fatal(err)
	}
	foreign := e.request(b, actGrant, payload{V: 1})
	aboutA := e.request(b, actGrant, payload{V: 1, Target: &a.StaffID})

	// откат модуля — гашение не остаётся
	_ = db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		if n, err := e.svc.CancelByInitiator(ctx, tx, a.StaffID); err != nil || n != 3 {
			t.Errorf("в откатываемой tx: n=%d err=%v", n, err)
		}
		return errFake("откат")
	})
	if e.row(mine[0].ID).Status != "pending" {
		t.Fatal("гашение пережило откат модуля")
	}

	var n int
	if err := db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = e.svc.CancelByInitiator(ctx, tx, a.StaffID)
		return err
	}); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, ap := range mine {
		r := e.row(ap.ID)
		if r.Status != "cancelled" || r.Label != approvals.SystemLabel || r.DecidedBy != nil ||
			r.Error != approvals.ErrorInitiatorChanged || r.DecidedAt == nil {
			t.Fatalf("погашенный: %+v", r)
		}
		if e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_cancelled' AND object_id = $1
			AND actor_role = 'system' AND actor_staff_id IS NULL`, ap.ID) != 1 || e.decided(ap.ID) != 1 {
			t.Fatal("нет аудита или события гашения")
		}
	}
	if e.row(done.ID).Status != "executed" || e.row(foreign.ID).Status != "pending" || e.row(aboutA.ID).Status != "pending" {
		t.Fatal("гашение задело лишнее")
	}
	// повтор — гасить нечего
	_ = db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		n, _ = e.svc.CancelByInitiator(ctx, tx, a.StaffID)
		return nil
	})
	if n != 0 {
		t.Fatalf("повтор погасил %d", n)
	}
}

// ExpireDue — просроченные ожидающие (expires_at <= now) → expired от системы порциями; живые и
// решённые не трогаются; аудит и событие на каждый.
func TestExpireDue(t *testing.T) {
	e := newEnv(t)
	restore := approvals.SetBatchForTest(2)
	defer restore()
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	var due []approvals.Approval
	for range 5 {
		due = append(due, e.request(a, actGrant, payload{V: 1}))
	}
	done := e.request(a, actGrant, payload{V: 1})
	if _, err := e.svc.Reject(context.Background(), b, "Борис", done.ID, "нет"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Hour)
	alive := e.request(a, actGrant, payload{V: 1})
	e.clock.Set(t0.Add(approvals.TTL)) // ровно срок первых шести; alive — через час
	n, err := e.svc.ExpireDue(context.Background())
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, ap := range due {
		if r := e.row(ap.ID); r.Status != "expired" || r.Label != approvals.SystemLabel || r.DecidedBy != nil || r.Error != "" {
			t.Fatalf("просроченный: %+v", r)
		}
		if e.audits(ap.ID, "platform.approval_expired") != 1 || e.decided(ap.ID) != 1 {
			t.Fatal("нет аудита или события просрочки")
		}
	}
	if e.row(alive.ID).Status != "pending" || e.row(done.ID).Status != "rejected" {
		t.Fatal("ExpireDue задел лишнее")
	}
	if n, err := e.svc.ExpireDue(context.Background()); err != nil || n != 0 {
		t.Fatalf("повтор: n=%d err=%v", n, err)
	}
}

// PurgeDecided — удаляет решённые старше границы; ожидающие (даже очень старые) и свежие решённые
// остаются; olderThan ≤ 0 — ошибка (не удаляет всё решённое разом).
func TestPurgeDecided(t *testing.T) {
	e := newEnv(t)
	restore := approvals.SetBatchForTest(2)
	defer restore()
	a, b := e.member(perm.Owner), e.member(perm.Owner)

	for range 3 {
		ap := e.request(a, actGrant, payload{V: 1})
		if _, err := e.svc.Reject(context.Background(), b, "Борис", ap.ID, "нет"); err != nil {
			t.Fatal(err)
		}

	}
	oldExpired := e.request(a, actGrant, payload{V: 1})
	oldPending := e.request(a, actGrant, payload{V: 1})
	e.clock.Advance(approvals.TTL)
	if _, err := e.svc.Approve(context.Background(), b, "Борис", oldExpired.ID, ""); !isErr(err, approvals.ErrClosed) {
		t.Fatal(err)
	}
	// oldPending — ожидает (в базе), хотя давно истёк: чистка его не трогает
	if _, err := e.owner.Exec(context.Background(), `UPDATE approvals SET expires_at = requested_at + interval '1 day' WHERE id = $1`, oldPending.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(approvals.DecidedRetention - approvals.TTL)
	fresh := e.request(a, actGrant, payload{V: 1})
	if _, err := e.svc.Reject(context.Background(), b, "Борис", fresh.ID, "нет"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(approvals.TTL + time.Second)
	if _, err := e.svc.PurgeDecided(context.Background(), 0); err == nil {
		t.Fatal("olderThan = 0 прошёл")
	}
	n, err := e.svc.PurgeDecided(context.Background(), approvals.DecidedRetention)
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if left := e.count(`SELECT count(*) FROM approvals`); left != 2 {
		t.Fatalf("осталось %d", left)
	}
	if e.row(oldPending.ID).Status != "pending" || e.row(fresh.ID).Status != "rejected" {
		t.Fatal("чистка задела ожидающий или свежий")
	}

}
