package events_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

// effects — побочный эффект подписчика в его транзакции: по нему видно, сколько раз он сработал.
func effects(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"CREATE TABLE test_effects (subscriber text, event_id uuid, version bigint)"); err != nil {
		t.Fatal(err)
	}
}

func record(name string) events.Handler {
	return func(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
		_, err := tx.Exec(ctx, "INSERT INTO test_effects VALUES ($1, $2, $3)", name, e.ID, e.AggregateVersion)
		return err
	}
}

func effectCount(t *testing.T, pool *pgxpool.Pool, subscriber string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM test_effects WHERE subscriber = $1", subscriber).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func publish(t *testing.T, pool *pgxpool.Pool, e events.Event) uuid.UUID {
	t.Helper()
	var eventID uuid.UUID
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		var err error
		eventID, err = events.Publish(ctx, e)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func job(eventID uuid.UUID, subscriber string) *river.Job[events.DeliverArgs] {
	return &river.Job[events.DeliverArgs]{JobRow: &rivertype.JobRow{}, Args: events.NewDeliverArgs(eventID, subscriber)}
}

func TestDeliverArgsConventions(t *testing.T) {
	a := events.NewDeliverArgs(id.New(), "stats.count")
	opts := a.InsertOpts()
	if a.Kind() != "events.deliver" || a.V != 1 || opts.Queue != events.QueueEvents ||
		!opts.UniqueOpts.ByArgs || opts.MaxAttempts == 0 {
		t.Fatalf("аргументы доставки: %+v, опции: %+v", a, opts)
	}
}

// Повтор доставки (River повторил задачу, relay переразложил) не задваивает обработку.
func TestRedeliveryIsProcessedOnce(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	reg, err := events.NewRegistry(events.Subscription{
		Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: record("stats.count"),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := events.NewDeliverWorker(pool, pool, reg)
	eventID := publish(t, pool, joined(id.New(), 1))
	for range 3 {
		if err := w.Work(context.Background(), job(eventID, "stats.count")); err != nil {
			t.Fatal(err)
		}
	}
	if n := effectCount(t, pool, "stats.count"); n != 1 {
		t.Fatalf("обработано %d раз", n)
	}
}

// Review Focus 3: ошибка обработчика откатывает и отметку inbox — повтор обрабатывает заново.
func TestHandlerErrorLeavesNoInbox(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	var fail atomic.Bool
	fail.Store(true)
	h := func(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
		if err := record("stats.count")(ctx, tx, e); err != nil {
			return err
		}
		if fail.Load() {
			return errors.New("временный сбой")
		}
		return nil
	}
	reg, _ := events.NewRegistry(events.Subscription{
		Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: h,
	})
	w := events.NewDeliverWorker(pool, pool, reg)
	eventID := publish(t, pool, joined(id.New(), 1))
	if err := w.Work(context.Background(), job(eventID, "stats.count")); err == nil {
		t.Fatal("ошибка обработчика проглочена")
	}
	if n := effectCount(t, pool, "stats.count"); n != 0 {
		t.Fatalf("эффект остался после ошибки: %d", n)
	}
	fail.Store(false)
	if err := w.Work(context.Background(), job(eventID, "stats.count")); err != nil {
		t.Fatal(err)
	}
	if n := effectCount(t, pool, "stats.count"); n != 1 {
		t.Fatalf("после повтора обработано %d раз", n)
	}
}

// LatestState отбрасывает событие со строго меньшей версией; EveryEvent применяет всё;
// события одной версии (одна транзакция агрегата) применяются оба (Решения контроллера).
func TestDeliveryModesAndStaleVersions(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	reg, err := events.NewRegistry(
		events.Subscription{Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: record("stats.count")},
		events.Subscription{Subscriber: "notify.roster", EventType: "teams.member_joined", Delivery: events.LatestState, Handle: record("notify.roster")},
	)
	if err != nil {
		t.Fatal(err)
	}
	w := events.NewDeliverWorker(pool, pool, reg)
	agg := id.New()
	v2 := publish(t, pool, joined(agg, 2))
	v2bis := publish(t, pool, joined(agg, 2))
	v1 := publish(t, pool, joined(agg, 1))
	// доставка в «неправильном» порядке: сначала новое, потом старое
	for _, eventID := range []uuid.UUID{v2, v2bis, v1} {
		for _, s := range []string{"stats.count", "notify.roster"} {
			if err := w.Work(context.Background(), job(eventID, s)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := effectCount(t, pool, "stats.count"); n != 3 {
		t.Fatalf("EveryEvent применил %d из 3", n)
	}
	if n := effectCount(t, pool, "notify.roster"); n != 2 {
		t.Fatalf("LatestState применил %d, ждали 2 (v2 и v2bis, без устаревшего v1)", n)
	}
	var cursor int64
	if err := pool.QueryRow(context.Background(),
		"SELECT version FROM event_cursors WHERE subscriber = 'notify.roster' AND aggregate_id = $1", agg).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("курсор = %d", cursor)
	}
	var stale int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM event_inbox WHERE subscriber = 'notify.roster' AND event_id = $1", v1).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 1 {
		t.Fatal("устаревшее событие не отмечено в inbox — его доставка повторялась бы")
	}
}

// Удалённое чисткой событие — отмена задачи, а не 20 бессмысленных повторов. Неизвестный
// подписчик — обычная ошибка с повтором: задачу мог взять воркер старого релиза при выкатке,
// отмена потеряла бы событие (River повторит, после исчерпания попыток задача — discarded).
func TestUnknownSubscriberRetriesDeletedEventCancels(t *testing.T) {
	pool := dbtest.NewPool(t)
	reg, _ := events.NewRegistry()
	w := events.NewDeliverWorker(pool, pool, reg)
	eventID := publish(t, pool, joined(id.New(), 1))

	var cancel *river.JobCancelError
	if err := w.Work(context.Background(), job(id.New(), "stats.count")); !errors.As(err, &cancel) {
		t.Errorf("удалённое событие: err = %v, ждали river.JobCancel", err)
	}
	err := w.Work(context.Background(), job(eventID, "stats.gone"))
	if err == nil {
		t.Fatal("неизвестный подписчик: ошибка проглочена")
	}
	if errors.As(err, &cancel) {
		t.Errorf("неизвестный подписчик: err = %v — отмена теряет событие, ждали ошибку с повтором", err)
	}
}

// lockWaiters — сколько сессий тестовой базы ждут блокировку.
func lockWaiters(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Две доставки одного агрегата одному LatestState-подписчику идут параллельно: v3 держит
// блокировку курсора (FOR UPDATE), пока её обработчик не отпущен; v2 ждёт на этой блокировке и,
// получив её, видит курсор 3 — отбрасывается как устаревшее. Курсор создан заранее доставкой v1:
// иначе вторую доставку сериализовал бы уже INSERT … ON CONFLICT в EnsureCursor, а не LockCursor.
func TestConcurrentLatestStateDropsStale(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	holding := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock) // при провале теста горутина v3 вернёт соединение до закрытия пула

	h := func(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
		if err := record("notify.roster")(ctx, tx, e); err != nil {
			return err
		}
		if e.AggregateVersion == 3 {
			close(holding)
			<-release
		}
		return nil
	}
	reg, err := events.NewRegistry(events.Subscription{
		Subscriber: "notify.roster", EventType: "teams.member_joined", Delivery: events.LatestState, Handle: h,
	})
	if err != nil {
		t.Fatal(err)
	}
	w := events.NewDeliverWorker(pool, pool, reg)
	agg := id.New()
	v1 := publish(t, pool, joined(agg, 1))
	v3 := publish(t, pool, joined(agg, 3))
	v2 := publish(t, pool, joined(agg, 2))
	ctx := context.Background()
	if err := w.Work(ctx, job(v1, "notify.roster")); err != nil {
		t.Fatal(err)
	}

	done3 := make(chan error, 1)
	go func() { done3 <- w.Work(ctx, job(v3, "notify.roster")) }()
	select {
	case <-holding:
	case err := <-done3:
		t.Fatalf("v3 завершилась, не дойдя до обработчика: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("v3 не дошла до обработчика")
	}

	// v2 стартует, пока v3 держит курсор. Отпускаем v3, только когда v2 либо встала на
	// блокировку (верное поведение), либо уже завершилась (блокировки нет — баг, ловит проверка ниже):
	// так исход не зависит от того, успела ли v2 дойти до курсора.
	done2 := make(chan error, 1)
	go func() { done2 <- w.Work(ctx, job(v2, "notify.roster")) }()
	var err2 error
	finished2 := false
	deadline := time.Now().Add(30 * time.Second)
	for !finished2 && lockWaiters(t, pool) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("v2 не встала на блокировку курсора и не завершилась")
		}
		select {
		case err2 = <-done2:
			finished2 = true
		case <-time.After(10 * time.Millisecond):
		}
	}
	unblock()
	if err := <-done3; err != nil {
		t.Fatalf("v3: %v", err)
	}
	if !finished2 {
		err2 = <-done2
	}
	if err2 != nil {
		t.Fatalf("v2: %v", err2)
	}

	rows, err := pool.Query(ctx, "SELECT version FROM test_effects WHERE subscriber = 'notify.roster' ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[0] != 1 || applied[1] != 3 {
		t.Fatalf("применены версии %v, ждали [1 3] — устаревшая v2 применена поверх v3", applied)
	}
	var cursor int64
	if err := pool.QueryRow(ctx,
		"SELECT version FROM event_cursors WHERE subscriber = 'notify.roster' AND aggregate_id = $1", agg).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 3 {
		t.Fatalf("курсор = %d, ждали 3", cursor)
	}
}
