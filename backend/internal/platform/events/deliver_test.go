package events_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

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

// Неизвестный подписчик (убран в новом релизе) и удалённое чисткой событие — отмена задачи,
// а не 20 бессмысленных повторов.
func TestUnknownSubscriberOrEventCancelsJob(t *testing.T) {
	pool := dbtest.NewPool(t)
	reg, _ := events.NewRegistry()
	w := events.NewDeliverWorker(pool, pool, reg)
	eventID := publish(t, pool, joined(id.New(), 1))
	for name, j := range map[string]*river.Job[events.DeliverArgs]{
		"подписчик": job(eventID, "stats.gone"),
		"событие":   job(id.New(), "stats.count"),
	} {
		var cancel *river.JobCancelError
		if err := w.Work(context.Background(), j); !errors.As(err, &cancel) {
			t.Errorf("%s: err = %v, ждали river.JobCancel", name, err)
		}
	}
}
