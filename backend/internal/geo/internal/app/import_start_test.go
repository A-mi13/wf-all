package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/internal/source"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Импорт из админки (Task 14): база на тест под ролью admin, клиент River только для вставки на том же пуле (как
// Deps.River admin-api), часы теста. Страны сида — RU (включена) и XA (выключена, своя вставка).

type jobEnv struct {
	owner, as *pgxpool.Pool
	clk       *clocktest.Fake
	rc        *river.Client[pgx.Tx]
}

func newJobEnv(t *testing.T) jobEnv {
	t.Helper()
	p := dbtest.NewPoolsAs(t, "admin")
	rc, err := river.NewClient(riverpgxv5.New(p.As), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	e := jobEnv{owner: p.Owner, as: p.As, clk: clocktest.New(tAct), rc: rc}
	e.exec(t, `INSERT INTO countries (code, name, currency, default_locale, phone_prefix, week_starts_on, min_signup_age,
	                                  age_of_majority, is_enabled)
	           VALUES ('XA', 'Тестовия', 'XXX', 'ru', '+999', 1, 14, 18, false)`)
	return e
}

func (e jobEnv) admin() *app.Admin {
	return app.NewAdmin(app.AdminDeps{Pool: e.as, Clock: e.clk, River: e.rc, Log: slog.New(slog.DiscardHandler)})
}

func (e jobEnv) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (e jobEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	return count(t, e.owner, sql, args...)
}

// inProgress — ошибка 409 geo.import_in_progress и её job_id (nil — без задачи).
func inProgress(t *testing.T, err error) *int64 {
	t.Helper()
	ipe, ok := errors.AsType[*geo.ImportInProgressError](err)
	if !ok || !errors.Is(err, geo.ErrImportInProgress) {
		t.Fatalf("ждали geo.ErrImportInProgress: %v", err)
	}
	return ipe.JobID
}

func (e jobEnv) state(t *testing.T, job int64) string {
	t.Helper()
	var s string
	if err := e.owner.QueryRow(context.Background(), `SELECT state FROM river_job WHERE id = $1`, job).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

var importMeta = approvals.Meta{UserAgent: "Mozilla/5.0 (test)"}

// Постановка: 202 (id задачи); в river_job.args ровно v, country, started_by (Q1); аудит geo.import_requested от
// сотрудника с объектом id.Named(geonames_import_job, job_id), after {country_code, job_id}, без города, UA запроса;
// повтор страны — 409 с job_id первой (уникальность River), задача одна; выключенная страна — можно.
func TestStartImport(t *testing.T) {
	e := newJobEnv(t)
	s := staffAs(perm.Admin)
	job, err := e.admin().StartImport(context.Background(), s, "RU", importMeta)
	if err != nil || job <= 0 {
		t.Fatalf("%d %v", job, err)
	}
	var raw []byte
	var kind, queueName string
	if err := e.owner.QueryRow(context.Background(), `SELECT args, kind, queue FROM river_job WHERE id = $1`, job).
		Scan(&raw, &kind, &queueName); err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(args)); !slices.Equal(got, []string{"country", "started_by", "v"}) ||
		args["country"] != "RU" || args["started_by"] != s.StaffID.String() || args["v"] != 1.0 ||
		kind != "geo.import" || queueName != queue.Maintenance {
		t.Fatalf("задача: %s %s %s", raw, kind, queueName)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_requested' AND actor_staff_id = $1
		AND actor_role = 'admin' AND actor_label = $2 AND object_type = 'geonames_import_job' AND object_id = $3
		AND scope_city_id IS NULL AND user_agent = $4 AND after = jsonb_build_object('country_code', 'RU', 'job_id', $5::bigint)`,
		s.StaffID, s.Label, id.Named("geonames_import_job", "1"), importMeta.UserAgent, job); n != 1 || job != 1 {
		t.Fatalf("аудит: %d (job %d)", n, job)
	}
	_, err = e.admin().StartImport(context.Background(), staffAs(perm.Owner), "RU", importMeta)
	if jid := inProgress(t, err); jid == nil || *jid != job {
		t.Fatalf("повтор: job_id %v, ждали %d", jid, job)
	}
	if n := e.count(t, `SELECT count(*) FROM river_job`); n != 1 {
		t.Fatalf("задач %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_requested'`); n != 1 {
		t.Fatalf("аудитов %d — дубль записал аудит", n)
	}
	// выключенная страна — импорт можно (спека geo §4.1)
	if _, err := e.admin().StartImport(context.Background(), s, "XA", importMeta); err != nil {
		t.Fatal(err)
	}
}

// Строка running страны: импорт оператора — 409 без job_id (задачи нет); строка задачи — 409 с её river_job_id.
// Задача не поставлена, аудита нет.
func TestStartImportRunningRow(t *testing.T) {
	e := newJobEnv(t)
	e.exec(t, `INSERT INTO geonames_imports (id, country_code, status, started_at) VALUES ($1, 'RU', 'running', $2)`, uuid.New(), tAct)
	_, err := e.admin().StartImport(context.Background(), staffAs(perm.Admin), "RU", importMeta)
	if jid := inProgress(t, err); jid != nil {
		t.Fatalf("оператор: job_id %d", *jid)
	}
	e.exec(t, `INSERT INTO geonames_imports (id, country_code, status, started_at, river_job_id) VALUES ($1, 'XA', 'running', $2, 777)`, uuid.New(), tAct)
	_, err = e.admin().StartImport(context.Background(), staffAs(perm.Admin), "XA", importMeta)
	if jid := inProgress(t, err); jid == nil || *jid != 777 {
		t.Fatalf("задача: job_id %v", jid)
	}
	if n := e.count(t, `SELECT count(*) FROM river_job`) + e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_requested'`); n != 0 {
		t.Fatalf("записано %d", n)
	}
}

// Нет страны — 404 geo.country_not_found; не ^[A-Z]{2}$ — 400 body.country (схема отвергнет раньше); модератор и
// поддержка — 403; ничего не записано.
func TestStartImportRejects(t *testing.T) {
	e := newJobEnv(t)
	if _, err := e.admin().StartImport(context.Background(), staffAs(perm.Admin), "ZZ", importMeta); !errors.Is(err, geo.ErrCountryNotFound) {
		t.Fatalf("нет страны: %v", err)
	}
	for _, bad := range []string{"ru", "RUS", "", "Ру"} {
		_, err := e.admin().StartImport(context.Background(), staffAs(perm.Admin), bad, importMeta)
		wantField(t, err, "body.country", "pattern")
	}
	for _, role := range []perm.Role{perm.Moderator, perm.Support} {
		_, err := e.admin().StartImport(context.Background(), staffAs(role), "RU", importMeta)
		wantForbidden(t, err)
	}
	if n := e.count(t, `SELECT count(*) FROM river_job`) + e.count(t, `SELECT count(*) FROM audit_log`); n != 0 {
		t.Fatalf("записано %d", n)
	}
}

// Две постановки одной страны одновременно: первая вставила задачу и ждёт (хук import_inserted), пока вторая не
// встанет на уникальном индексе River; первая коммитит — вторая получает дубль: 409 с job_id первой. Одна задача,
// один аудит.
func TestStartImportConcurrent(t *testing.T) {
	e := newJobEnv(t)
	var doneOnce sync.Once
	var first atomic.Bool
	var waited bool
	anyDone := make(chan struct{})
	a := e.admin()
	a.SetStageHook(func(_ context.Context, stage string) {
		if stage == "import_inserted" {
			if first.CompareAndSwap(false, true) { // остальные хук проходят сразу (sync.Once держал бы их)
				waited = waitLockOrDoneWithin(t, e.owner, anyDone, 10*time.Second)
			}
		}
	})
	start := make(chan struct{})
	var wg sync.WaitGroup
	jobs, errs := make([]int64, 2), make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			jobs[i], errs[i] = a.StartImport(context.Background(), staffAs(perm.Admin), "RU", importMeta)
			doneOnce.Do(func() { close(anyDone) })
		})
	}
	close(start)
	wg.Wait()
	ok, busy := 0, 1
	if errs[0] != nil {
		ok, busy = 1, 0
	}
	if errs[ok] != nil {
		t.Fatalf("обе с ошибкой: %v / %v", errs[0], errs[1])
	}
	if jid := inProgress(t, errs[busy]); jid == nil || *jid != jobs[ok] {
		t.Fatalf("вторая: job_id %v, ждали %d", jid, jobs[ok])
	}
	if n := e.count(t, `SELECT count(*) FROM river_job`); n != 1 {
		t.Fatalf("задач %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_requested'`); n != 1 {
		t.Fatalf("аудитов %d", n)
	}
	if !waited {
		t.Fatal("вторая не встала на замке — гонки не было")
	}
}

// Отмена задачи «в очереди»: cancelled, аудит geo.import_cancelled от сотрудника (объект — задача), уникальность
// страны освобождена (новая постановка — 202); неизвестный id — 404.
func TestCancelImportJob(t *testing.T) {
	e := newJobEnv(t)
	s := staffAs(perm.Admin)
	job, err := e.admin().StartImport(context.Background(), s, "RU", importMeta)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.admin().CancelImportJob(context.Background(), s, job, importMeta)
	if err != nil || got.State != geo.JobCancelled || got.JobID != job || got.MaxAttempts != 3 || got.ImportID != nil {
		t.Fatalf("%+v %v", got, err)
	}
	if st := e.state(t, job); st != "cancelled" {
		t.Fatalf("состояние %s", st)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_cancelled' AND actor_staff_id = $1
		AND object_type = 'geonames_import_job' AND object_id = $2 AND scope_city_id IS NULL AND user_agent = $3
		AND after = jsonb_build_object('country_code', 'RU', 'job_id', $4::bigint)`,
		s.StaffID, id.Named("geonames_import_job", "1"), importMeta.UserAgent, job); n != 1 {
		t.Fatalf("аудит: %d", n)
	}
	again, err := e.admin().StartImport(context.Background(), s, "RU", importMeta)
	if err != nil || again == job {
		t.Fatalf("после отмены: %d %v", again, err)
	}
	if _, err := e.admin().CancelImportJob(context.Background(), s, 999999, importMeta); !errors.Is(err, geo.ErrImportJobNotFound) {
		t.Fatalf("нет задачи: %v", err)
	}
	for _, role := range []perm.Role{perm.Moderator, perm.Support} {
		_, err := e.admin().CancelImportJob(context.Background(), staffAs(role), again, importMeta)
		wantForbidden(t, err)
	}
}

// K6: вторая отмена той же задачи (новый ключ, второй сотрудник) — 200 cancelled, записей geo.import_cancelled по
// задаче ровно одна.
func TestCancelImportJobAlreadyCancelled(t *testing.T) {
	e := newJobEnv(t)
	job, err := e.admin().StartImport(context.Background(), staffAs(perm.Admin), "RU", importMeta)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*auth.Staff{staffAs(perm.Admin), staffAs(perm.Owner)} {
		got, err := e.admin().CancelImportJob(context.Background(), s, job, importMeta)
		if err != nil || got.State != geo.JobCancelled {
			t.Fatalf("%+v %v", got, err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_cancelled' AND object_id = $1`,
		id.Named("geonames_import_job", "1")); n != 1 {
		t.Fatalf("аудитов %d", n)
	}
}

// K6, повторное ревью G4: две отмены одной queued-задачи одновременно — первая под замком задачи ждёт (хук
// cancel_locked), пока вторая не встанет на том же замке; обе 200 cancelled, аудит один: «отменил этот вызов» и «уже
// была отменена» различаются под замком, а не чтением до него.
func TestCancelImportJobConcurrent(t *testing.T) {
	e := newJobEnv(t)
	job, err := e.admin().StartImport(context.Background(), staffAs(perm.Admin), "RU", importMeta)
	if err != nil {
		t.Fatal(err)
	}
	var doneOnce sync.Once
	var first atomic.Bool
	var waited bool
	anyDone := make(chan struct{})
	a := e.admin()
	a.SetStageHook(func(_ context.Context, stage string) {
		if stage == "cancel_locked" {
			if first.CompareAndSwap(false, true) { // остальные хук проходят сразу (sync.Once держал бы их)
				waited = waitLockOrDoneWithin(t, e.owner, anyDone, 10*time.Second)
			}
		}
	})
	start := make(chan struct{})
	var wg sync.WaitGroup
	got, errs := make([]geo.ImportJob, 2), make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			got[i], errs[i] = a.CancelImportJob(context.Background(), staffAs(perm.Admin), job, importMeta)
			doneOnce.Do(func() { close(anyDone) })
		})
	}
	close(start)
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil || got[i].State != geo.JobCancelled {
			t.Fatalf("отмена %d: %+v %v", i, got[i], errs[i])
		}
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_cancelled'`); n != 1 {
		t.Fatalf("аудитов %d", n)
	}
	if !waited {
		t.Fatal("вторая не встала на замке — гонки не было")
	}
}

// I-1: задачу взял исполнитель (running) — отмена 409 geo.import_job_not_cancellable с откатом: у строки нет
// metadata.cancel_attempted_at, уведомления отмены River не было (LISTEN канала управления пуст), аудита нет.
// Контроль слушателя: pg_notify в тот же канал доходит.
func TestCancelImportJobRaceWithFetch(t *testing.T) {
	e := newJobEnv(t)
	ctx := context.Background()
	job, err := e.admin().StartImport(ctx, staffAs(perm.Admin), "RU", importMeta)
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE river_job SET state = 'running', attempt = 1, attempted_at = now(), attempted_by = ARRAY['test'] WHERE id = $1`, job)
	conn, err := e.owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var channel string
	if err := conn.QueryRow(ctx, `SELECT current_schema() || '.river_control'`).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	_, err = e.admin().CancelImportJob(ctx, staffAs(perm.Admin), job, importMeta)
	if !errors.Is(err, geo.ErrImportJobNotCancellable) {
		t.Fatalf("ждали geo.ErrImportJobNotCancellable: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM river_job WHERE id = $1 AND state = 'running' AND NOT metadata ? 'cancel_attempted_at'`, job); n != 1 {
		t.Fatal("метка отмены осталась или состояние изменилось")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'geo.import_cancelled'`); n != 0 {
		t.Fatal("аудит отмены")
	}
	wctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	n, err := conn.Conn().WaitForNotification(wctx)
	cancel()
	if err == nil {
		t.Fatalf("уведомление отмены ушло: %s", n.Payload)
	}
	if _, err := e.owner.Exec(ctx, `SELECT pg_notify($1, 'probe')`, channel); err != nil {
		t.Fatal(err)
	}
	wctx, cancel = context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if n, err := conn.Conn().WaitForNotification(wctx); err != nil || n.Payload != "probe" {
		t.Fatalf("слушатель не работает: %v", err)
	}
}

// Задача завершена (completed) или выброшена (discarded) — 409, состояние не меняется.
func TestCancelImportJobFinished(t *testing.T) {
	e := newJobEnv(t)
	for _, st := range []string{"completed", "discarded"} {
		job, err := e.admin().StartImport(context.Background(), staffAs(perm.Admin), "RU", importMeta)
		if err != nil {
			t.Fatal(err)
		}
		e.exec(t, `UPDATE river_job SET state = $2::river_job_state, finalized_at = now() WHERE id = $1`, job, st)
		if _, err := e.admin().CancelImportJob(context.Background(), staffAs(perm.Admin), job, importMeta); !errors.Is(err, geo.ErrImportJobNotCancellable) {
			t.Fatalf("%s: %v", st, err)
		}
		if got := e.state(t, job); got != st {
			t.Fatalf("%s → %s", st, got)
		}
	}
}

type mailLike struct{}

func (mailLike) Kind() string { return "staff.mail" }

// I-1: id задачи другого вида (staff.mail) — 404 geo.import_job_not_found и для карточки, и для отмены; задача
// осталась available, метки отмены нет.
func TestCancelImportJobForeignKind(t *testing.T) {
	e := newJobEnv(t)
	res, err := e.rc.Insert(context.Background(), mailLike{}, &river.InsertOpts{Queue: queue.Staff})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin().CancelImportJob(context.Background(), staffAs(perm.Admin), res.Job.ID, importMeta); !errors.Is(err, geo.ErrImportJobNotFound) {
		t.Fatalf("отмена: %v", err)
	}
	if _, err := e.admin().ImportJob(context.Background(), staffAs(perm.Admin), res.Job.ID); !errors.Is(err, geo.ErrImportJobNotFound) {
		t.Fatalf("карточка: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM river_job WHERE id = $1 AND state = 'available' AND NOT metadata ? 'cancel_attempted_at'`, res.Job.ID); n != 1 {
		t.Fatal("чужая задача изменена")
	}
}

// Состояния River → контракт (R7): ждущие — queued, discarded — failed; import_id — последняя строка журнала задачи.
func TestImportJobState(t *testing.T) {
	e := newJobEnv(t)
	s := staffAs(perm.Support) // поддержка читает (geo.imports.read)
	job, err := e.admin().StartImport(context.Background(), staffAs(perm.Admin), "RU", importMeta)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ river, want, finalized string }{
		{"available", geo.JobQueued, "NULL"}, {"scheduled", geo.JobQueued, "NULL"}, {"retryable", geo.JobQueued, "NULL"},
		{"pending", geo.JobQueued, "NULL"}, {"running", geo.JobRunning, "NULL"}, {"completed", geo.JobCompleted, "now()"},
		{"discarded", geo.JobFailed, "now()"}, {"cancelled", geo.JobCancelled, "now()"},
	} {
		e.exec(t, `UPDATE river_job SET state = $2::river_job_state, finalized_at = `+c.finalized+` WHERE id = $1`, job, c.river)
		got, err := e.admin().ImportJob(context.Background(), s, job)
		if err != nil || got.State != c.want || got.JobID != job {
			t.Fatalf("%s: %+v %v", c.river, got, err)
		}
	}
	old, last := uuid.New(), uuid.New()
	e.exec(t, `INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at, river_job_id)
	           VALUES ($1, 'RU', 'failed', $3, $3, $4), ($2, 'RU', 'succeeded', $3::timestamptz + interval '1 minute', $3, $4)`,
		old, last, tAct, job)
	if got, err := e.admin().ImportJob(context.Background(), s, job); err != nil || got.ImportID == nil || *got.ImportID != last {
		t.Fatalf("import_id: %+v %v", got, err)
	}
	if _, err := e.admin().ImportJob(context.Background(), s, 424242); !errors.Is(err, geo.ErrImportJobNotFound) {
		t.Fatalf("нет задачи: %v", err)
	}
	if _, err := e.admin().ImportJob(context.Background(), staffAs(perm.Moderator), job); !isForbidden(err) {
		t.Fatalf("модератор: %v", err)
	}
}

// После постановки — queued, попыток 0; тестовый исполнитель River (настоящий импорт на выдержке GeoNames под
// пулом владельца) выполняет задачу — completed и import_id строки журнала этой задачи.
func TestGetGeoImportJob(t *testing.T) {
	e := newJobEnv(t)
	s := staffAs(perm.Admin)
	job, err := e.admin().StartImport(context.Background(), s, "RU", importMeta)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.admin().ImportJob(context.Background(), s, job)
	if err != nil || got.State != geo.JobQueued || got.Attempt != 0 || got.MaxAttempts != 3 || got.ImportID != nil || got.CreatedAt.IsZero() {
		t.Fatalf("в очереди: %+v %v", got, err)
	}
	raw := fixture(t)
	im := app.NewImporter(e.owner, e.clk, source.FetchConfig{}, slog.New(slog.DiscardHandler))
	im.SetFetch(func(context.Context, source.FetchConfig, string) (source.Raw, error) { return raw, nil })
	workers := river.NewWorkers()
	river.AddWorker(workers, river.WorkFunc(func(ctx context.Context, j *river.Job[app.ImportArgs]) error {
		_, err := im.Import(ctx, j.Args.Country, j.Args.StartedBy, &j.ID)
		return err
	}))
	wc, err := river.NewClient(riverpgxv5.New(e.owner), &river.Config{
		Queues: map[string]river.QueueConfig{queue.Maintenance: {MaxWorkers: 1}}, Workers: workers,
		FetchCooldown: 10 * time.Millisecond, FetchPollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, stop := wc.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := wc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wc.Stop(context.Background()) }()
	select {
	case ev := <-events:
		if ev.Kind != river.EventKindJobCompleted || ev.Job.ID != job {
			t.Fatalf("исполнение: %s %+v", ev.Kind, ev.Job)
		}
	case <-ctx.Done():
		t.Fatal("задача не выполнена за 60 с")
	}
	got, err = e.admin().ImportJob(context.Background(), s, job)
	if err != nil || got.State != geo.JobCompleted || got.ImportID == nil || got.Attempt != 1 {
		t.Fatalf("выполнена: %+v %v", got, err)
	}
	if n := e.count(t, `SELECT count(*) FROM geonames_imports WHERE id = $1 AND river_job_id = $2 AND status = 'succeeded'
		AND started_by = $3`, *got.ImportID, job, s.StaffID); n != 1 {
		t.Fatal("строка журнала не та")
	}
}

func isForbidden(err error) bool {
	status, code, _ := problem(err)
	return status == http.StatusForbidden && code == httpx.CodeForbidden
}

// m1 ревью G5: created_at записи постановки — время базы (now() транзакции admin-api), started_at строки — часы
// воркера; часы воркера отстают на 100 мс — подпись всё равно находится (допуск расхождения часов). Запись позже
// started_at больше допуска — не этой строки (повтор river_job.id после сброса последовательности) — подписи нет.
func TestListImportsStartedByClockSkew(t *testing.T) {
	e := newJobEnv(t)
	ins := func(job int64, started, recorded time.Time, label string) uuid.UUID {
		rid := uuid.New()
		e.exec(t, `INSERT INTO geonames_imports (id, country_code, status, started_at, finished_at, started_by, river_job_id)
		           VALUES ($1, 'RU', 'succeeded', $2, $2, $3, $4)`, rid, started, uuid.New(), job)
		e.exec(t, `INSERT INTO audit_log (actor_staff_id, actor_role, actor_label, action, object_type, object_id, created_at)
		           VALUES ($1, 'admin', $2, 'geo.import_requested', 'geonames_import_job', $3, $4)`,
			uuid.New(), label, id.Named("geonames_import_job", fmt.Sprint(job)), recorded)
		return rid
	}
	skewed := ins(5001, tAct, tAct.Add(100*time.Millisecond), "Часы отстают")
	late := ins(5002, tAct.Add(-time.Hour), tAct.Add(-time.Hour+2*time.Minute), "Позже допуска")
	pg, err := app.NewAdminReader(e.as).Imports(context.Background(), geo.ImportQuery{Country: "RU"})
	if err != nil {
		t.Fatal(err)
	}
	l := labels(pg.Items)
	if v := l[skewed]; v == nil || *v != "Часы отстают" {
		t.Errorf("расхождение 100 мс: %v", v)
	}
	if v := l[late]; v != nil {
		t.Errorf("запись позже допуска: %v", *v)
	}
}
