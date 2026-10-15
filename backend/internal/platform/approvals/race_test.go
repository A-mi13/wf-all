package approvals_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/perm"
)

// holdRow — отдельная транзакция держит замок строки запроса до release: решения, пришедшие за
// замком, встают в очередь, и release отпускает их одновременно.
func (e *env) holdRow(apID uuid.UUID) (release func()) {
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

// waitBlocked — ждёт, пока n соединений базы теста встанут в ожидание замка (опрос условия, а не
// пауза наугад). Без t: зовётся и из горутин.
func (e *env) waitBlocked(n int) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got int
		if err := e.owner.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&got); err != nil {
			return err
		}
		if got >= n {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("за 10 с в ожидание замка встали %d соединений из %d", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) mustBlocked(n int) {
	e.t.Helper()
	if err := e.waitBlocked(n); err != nil {
		e.t.Fatal(err)
	}
}

// race — f и g стартуют вместе (общий барьер) и ждут друг друга.
func race(f, g func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, fn := range []func(){f, g} {
		wg.Go(func() {
			<-start
			fn()
		})
	}
	close(start)
	wg.Wait()
}

// Два подтверждающих одновременно — ровно одно исполнение; второй получает ErrClosed.
func TestApproveConcurrentOnce(t *testing.T) {
	e := newEnv(t)
	a, b, c := e.member(perm.Owner), e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	release := e.holdRow(ap.ID)
	var errB, errC error
	done := make(chan struct{})
	go func() {
		race(func() { _, errB = e.svc.Approve(context.Background(), b, "Борис", ap.ID, "") },
			func() { _, errC = e.svc.Approve(context.Background(), c, "Вера", ap.ID, "") })
		close(done)
	}()
	e.mustBlocked(2)
	release()
	<-done
	ok, closed := 0, 0
	for _, err := range []error{errB, errC} {
		switch {
		case err == nil:
			ok++
		case isErr(err, approvals.ErrClosed):
			closed++
		default:
			t.Fatalf("ошибка: %v", err)
		}
	}
	if ok != 1 || closed != 1 || e.execs.Load() != 1 || e.decided(ap.ID) != 1 ||
		e.audits(ap.ID, "platform.approval_executed") != 1 || e.moduleWrites() != 2 {
		t.Fatalf("успехов %d, закрыто %d, исполнений %d, событий %d", ok, closed, e.execs.Load(), e.decided(ap.ID))
	}
}

// Подтверждение и отмена инициатором одновременно — один исход, второй получает ErrClosed.
func TestApproveCancelRace(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	release := e.holdRow(ap.ID)
	var errApprove, errCancel error
	done := make(chan struct{})
	go func() {
		race(func() { _, errApprove = e.svc.Approve(context.Background(), b, "Борис", ap.ID, "") },
			func() { _, errCancel = e.svc.Cancel(context.Background(), a, "Анна", ap.ID) })
		close(done)
	}()
	e.mustBlocked(2)
	release()
	<-done
	r := e.row(ap.ID)
	switch {
	case errApprove == nil && isErr(errCancel, approvals.ErrClosed):
		if r.Status != "executed" || e.execs.Load() != 1 {
			t.Fatalf("победило подтверждение: %+v, исполнений %d", r, e.execs.Load())
		}
	case errCancel == nil && isErr(errApprove, approvals.ErrClosed):
		if r.Status != "cancelled" || e.execs.Load() != 0 {
			t.Fatalf("победила отмена: %+v, исполнений %d", r, e.execs.Load())
		}
	default:
		t.Fatalf("approve=%v cancel=%v", errApprove, errCancel)
	}
	if e.decided(ap.ID) != 1 {
		t.Fatalf("событий %d", e.decided(ap.ID))
	}
}

// Срок вышел, подтверждение и ExpireDue одновременно: один переход в expired — одна запись аудита
// и одно событие; подтверждение — ErrClosed, исполнителя нет.
func TestApproveExpireRace(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	e.clock.Set(ap.ExpiresAt)
	var errApprove, errExpire error
	var n int
	race(func() { _, errApprove = e.svc.Approve(context.Background(), b, "Борис", ap.ID, "") },
		func() { n, errExpire = e.svc.ExpireDue(context.Background()) })
	if errExpire != nil || !isErr(errApprove, approvals.ErrClosed) || n > 1 {
		t.Fatalf("approve=%v expire=%v n=%d", errApprove, errExpire, n)
	}
	if r := e.row(ap.ID); r.Status != "expired" || e.execs.Load() != 0 {
		t.Fatalf("строка: %+v", r)
	}
	if e.audits(ap.ID, "platform.approval_expired") != 1 || e.decided(ap.ID) != 1 {
		t.Fatalf("аудит %d, событий %d", e.audits(ap.ID, "platform.approval_expired"), e.decided(ap.ID))
	}
}

// Строку под замком решения ExpireDue не ждёт (SKIP LOCKED) — её переводит само решение.
func TestExpireDueSkipsLocked(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	e.clock.Set(ap.ExpiresAt)
	release := e.holdRow(ap.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if n, err := e.svc.ExpireDue(ctx); err != nil || n != 0 {
		t.Fatalf("ExpireDue под замком: n=%d err=%v", n, err)
	}
	release()
	if _, err := e.svc.Approve(context.Background(), b, "Борис", ap.ID, ""); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("approve: %v", err)
	}
	if e.audits(ap.ID, "platform.approval_expired") != 1 {
		t.Fatal("нет аудита просрочки")
	}
}

// staffLock — ключ замка учётки инициатора в модели пути staff (8b: выключение берёт замок учётки,
// проверка инициатора читает её под FOR SHARE).
const staffLock = 8_100_001

// Выключение инициатора (замок учётки → CancelByInitiator) параллельно с подтверждением его запроса
// (замок строки → InitiatorCheck ждёт замок учётки) — без взаимной блокировки: гашение пропускает
// строку под замком, подтверждение после выключения видит инициатора выключенным — failed + ErrStale.
func TestCancelByInitiatorNoDeadlock(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	inCheck := make(chan struct{})
	e.mu.Lock()
	e.check = func(ctx context.Context, tx pgx.Tx, staffID uuid.UUID, permission string) error {
		close(inCheck) // строка запроса уже под замком Approve
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, staffLock); err != nil {
			return err
		}
		e.mu.Lock()
		e.check = nil
		e.mu.Unlock()
		return e.initiatorCheck(ctx, tx, staffID, permission)
	}
	e.mu.Unlock()

	locked := make(chan struct{})
	var cancelled int
	var errDisable error
	disabled := make(chan struct{})
	go func() {
		defer close(disabled)
		errDisable = db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, staffLock); err != nil {
				return err
			}
			close(locked)
			<-inCheck
			if err := e.waitBlocked(1); err != nil { // Approve ждёт замок учётки
				return err
			}
			var err error
			cancelled, err = e.svc.CancelByInitiator(ctx, tx, a.StaffID)
			e.disable(a.StaffID)
			return err
		})
	}()
	<-locked
	res, err := e.svc.Approve(context.Background(), b, "Борис", ap.ID, "")
	<-disabled
	if errDisable != nil {
		t.Fatalf("выключение: %v", errDisable)
	}
	if !isErr(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || cancelled != 0 {
		t.Fatalf("approve=%v %+v, погашено %d", err, res, cancelled)
	}
	if e.execs.Load() != 0 || e.decided(ap.ID) != 1 {
		t.Fatalf("исполнений %d, событий %d", e.execs.Load(), e.decided(ap.ID))
	}
}
