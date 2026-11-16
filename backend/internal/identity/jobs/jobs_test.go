package jobs

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/testkit/rivertest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// periodicID — ID периодической задачи (River его не отдаёт; reflect только читает строку).
func periodicID(j *river.PeriodicJob) string {
	return reflect.ValueOf(j).Elem().FieldByName("opts").Elem().FieldByName("ID").String()
}

// Периодика identity: три вида (ID = вид), в очереди maintenance, окно уникальности — интервал запуска (длиннее —
// глотало бы запуски); новый срез и новые задачи на каждый вызов.
func TestPeriodic(t *testing.T) {
	t.Parallel()
	a, b := Periodic(), Periodic()
	var ids []string
	for i, j := range a {
		if j == nil || j == b[i] {
			t.Fatalf("задача %d: %p и %p — ждали новую на каждый вызов", i, j, b[i])
		}
		ids = append(ids, periodicID(j))
	}
	slices.Sort(ids)
	if want := []string{"identity.anonymize", "identity.maintenance", "identity.mfa_reset_execute"}; !slices.Equal(ids, want) {
		t.Fatalf("периодика %v, ждали %v", ids, want)
	}
	for _, c := range []struct {
		args river.JobArgsWithInsertOpts
		kind string
		per  time.Duration
	}{
		{MaintenanceArgs{}, MaintenanceArgs{}.Kind(), 10 * time.Minute},
		{AnonymizeArgs{}, AnonymizeArgs{}.Kind(), time.Hour},
		{MfaResetExecuteArgs{}, MfaResetExecuteArgs{}.Kind(), 10 * time.Minute},
	} {
		o := c.args.InsertOpts()
		if o.Queue != queue.Maintenance || o.UniqueOpts.ByPeriod != c.per {
			t.Errorf("%s: очередь %q, окно %s; ждали %q и %s", c.kind, o.Queue, o.UniqueOpts.ByPeriod, queue.Maintenance, c.per)
		}
	}
}

// AddWorkers — ровно три исполнителя identity; без зависимостей — ошибка сборки, а не паника в задаче.
func TestAddWorkers(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "worker")
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pools.As), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	w := river.NewWorkers()
	if err := AddWorkers(w, identity.WorkerDeps{Pool: pools.As, Clock: clocktest.New(time.Now()), River: ins, Log: log}); err != nil {
		t.Fatal(err)
	}
	want := []string{"identity.anonymize", "identity.maintenance", "identity.mfa_reset_execute"}
	if got := rivertest.Kinds(w); !slices.Equal(got, want) {
		t.Fatalf("исполнители %v, ждали %v", got, want)
	}
	if err := AddWorkers(river.NewWorkers(), identity.WorkerDeps{}); err == nil {
		t.Fatal("сборка без зависимостей прошла")
	}
}

// fakeCleaner — чистка с заданным итогом.
type fakeCleaner struct {
	calls int
	err   error
}

func (f *fakeCleaner) Cleanup(context.Context) (app.CleanupReport, error) {
	f.calls++
	return app.CleanupReport{Rows: map[string]int{"auth_codes": 3}}, f.err
}

func maintenanceJob(v int) *river.Job[MaintenanceArgs] {
	return &river.Job[MaintenanceArgs]{JobRow: &rivertype.JobRow{}, Args: MaintenanceArgs{V: v}}
}

// Исполнитель чистки: ошибка Cleanup — ошибка задачи (повтор River); чужая версия — отказ без чистки.
func TestMaintenanceWorker(t *testing.T) {
	t.Parallel()
	f := &fakeCleaner{}
	w := NewMaintenanceWorker(f, slog.New(slog.DiscardHandler))
	if err := w.Work(context.Background(), maintenanceJob(1)); err != nil || f.calls != 1 {
		t.Fatalf("%v, вызовов %d", err, f.calls)
	}
	f.err = errors.New("сбой")
	if err := w.Work(context.Background(), maintenanceJob(1)); !errors.Is(err, f.err) {
		t.Fatalf("ошибка чистки не дошла: %v", err)
	}
	if err := w.Work(context.Background(), maintenanceJob(2)); !errors.Is(err, errMaintenanceVersion) || f.calls != 2 {
		t.Fatalf("v=2: %v, вызовов %d", err, f.calls)
	}
}

// m4: к сроку сброса 2FA аккаунт в периоде удаления — исполнитель из сборки воркера (AddWorkers → app под ролью
// worker) сброс не исполняет: cancelled_by = system, 2FA и сессии на месте, письма нет.
func TestExecuteSkipsDeleted(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var uid uuid.UUID
	if err := pools.Owner.QueryRow(ctx, `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale, status,
		status_before_deletion, deletion_requested_at, purge_at)
		VALUES ('skip_deleted@example.com', now(), 'skip_deleted', '2000-01-01', 'ru', 'pending_deletion', 'active', $1, $2)
		RETURNING id`, now.Add(-time.Hour), now.Add(29*24*time.Hour)).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, '\x01', 'AAAAAAAAAAA', $2, $2)`, uid, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rid := uuid.New()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id,
		requested_by_label, reason, execute_at, created_at) VALUES ($1, $2, gen_random_uuid(), gen_random_uuid(), 'Оператор',
		'потерял телефон', $3, $4)`, rid, uid, now.Add(-time.Minute), now.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pools.As), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	a, err := maintenanceApp(identity.WorkerDeps{Pool: pools.As, Clock: clocktest.New(now), River: ins, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	job := &river.Job[MfaResetExecuteArgs]{JobRow: &rivertype.JobRow{}, Args: MfaResetExecuteArgs{V: 1}}
	if err := NewMfaResetWorker(a).Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	one := func(q string, args ...any) int {
		var n int
		if err := pools.Owner.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if one(`SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_by = 'system' AND executed_at IS NULL`, rid) != 1 {
		t.Fatal("сброс не отменён системой")
	}
	if one(`SELECT count(*) FROM user_mfa WHERE user_id = $1`, uid) != 1 {
		t.Fatal("2FA снята у аккаунта в удалении")
	}
	if n := one(`SELECT count(*) FROM river_job WHERE kind = 'mail.send'`); n != 0 {
		t.Fatalf("писем: %d", n)
	}
}
