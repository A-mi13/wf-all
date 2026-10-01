package events_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

var discard = slog.New(slog.DiscardHandler)

// inserter — клиент River только для вставки: задачи ложатся в river_job, никто их не исполняет.
func inserter(t *testing.T, pool *pgxpool.Pool) *river.Client[pgx.Tx] {
	t.Helper()
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: discard})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func deliveries(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM river_job WHERE kind = 'events.deliver'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func unpublished(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func twoSubscribers(t *testing.T) *events.Registry {
	t.Helper()
	reg, err := events.NewRegistry(
		events.Subscription{Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: noop},
		events.Subscription{Subscriber: "notify.roster", EventType: "teams.member_joined", Delivery: events.LatestState, Handle: noop},
	)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func left(agg uuid.UUID) events.Event {
	e := joined(agg, 1)
	e.Type = "teams.member_left"
	return e
}

func TestDrainEnqueuesPerSubscriberAndMarksPublished(t *testing.T) {
	pool := dbtest.NewPool(t)
	for range 3 {
		publish(t, pool, joined(id.New(), 1))
	}
	publish(t, pool, left(id.New())) // без подписчиков — просто отмечается
	r := events.NewRelay(pool, inserter(t, pool), twoSubscribers(t), discard, events.RelayConfig{Batch: 2, Poll: time.Hour})
	n, err := r.Drain(context.Background())
	if err != nil || n != 4 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
	if got := deliveries(t, pool); got != 6 {
		t.Fatalf("задач доставки %d, ждали 3 события × 2 подписчика", got)
	}
	if got := unpublished(t, pool); got != 0 {
		t.Fatalf("неопубликованных %d", got)
	}
}

type failingInserter struct{}

func (failingInserter) InsertManyTx(context.Context, pgx.Tx, []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	return nil, errors.New("river недоступен")
}

// Review Focus 1: постановка задач и отметка published_at — одна транзакция.
func TestInsertErrorRollsBackMarking(t *testing.T) {
	pool := dbtest.NewPool(t)
	publish(t, pool, joined(id.New(), 1))
	r := events.NewRelay(pool, failingInserter{}, twoSubscribers(t), discard, events.RelayConfig{Batch: 10, Poll: time.Hour})
	if _, err := r.Drain(context.Background()); err == nil {
		t.Fatal("ошибка вставки задач проглочена")
	}
	if got := unpublished(t, pool); got != 1 {
		t.Fatalf("событие отмечено опубликованным без задач: неопубликованных %d", got)
	}
}

// warm открывает n соединений пула заранее: иначе второй relay стартует позже, пока
// устанавливается соединение, и гонки за строки не случается.
func warm(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	conns := make([]*pgxpool.Conn, 0, n)
	for range n {
		c, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Release()
	}
}

// Review Focus 2: два relay (два инстанса воркера) не ставят одно событие дважды.
func TestTwoRelaysEnqueueEachEventOnce(t *testing.T) {
	pool := dbtest.NewPool(t)
	for range 200 {
		publish(t, pool, joined(id.New(), 1))
	}
	reg, _ := events.NewRegistry(events.Subscription{
		Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: noop,
	})
	ins := inserter(t, pool)
	warm(t, pool, 2)
	var wg sync.WaitGroup
	var drained atomic.Int64
	for range 2 {
		r := events.NewRelay(pool, ins, reg, discard, events.RelayConfig{Batch: 7, Poll: time.Hour})
		wg.Go(func() {
			n, err := r.Drain(context.Background())
			if err != nil {
				t.Error(err)
			}
			drained.Add(int64(n))
		})
	}
	wg.Wait()
	// Уникальность задач по аргументам прячет дубли в river_job, поэтому отдельно: каждое
	// событие взял ровно один relay (без SKIP LOCKED оба читают одни и те же строки).
	if got := drained.Load(); got != 200 {
		t.Fatalf("relay взяли %d событий на двоих, ждали 200 — строки не делятся", got)
	}
	var total, distinct int
	if err := pool.QueryRow(context.Background(), `SELECT count(*), count(DISTINCT args->>'event_id')
		FROM river_job WHERE kind = 'events.deliver'`).Scan(&total, &distinct); err != nil {
		t.Fatal(err)
	}
	if total != 200 || distinct != 200 {
		t.Fatalf("задач %d, разных событий %d — ждали по 200", total, distinct)
	}
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("не дождались: %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runRelay(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	runRelayWith(t, pool, inserter(t, pool), twoSubscribers(t))
}

// Review Focus 5: опрос — раз в час, доставка — по NOTIFY.
func TestRunWakesOnNotify(t *testing.T) {
	pool := dbtest.NewPool(t)
	runRelay(t, pool)
	publish(t, pool, joined(id.New(), 1))
	waitFor(t, "задачи по NOTIFY", 5*time.Second, func() bool { return deliveries(t, pool) == 2 })
}

// Review Focus 5: обрыв LISTEN-соединения — relay переподключается и не теряет событий.
func TestRunSurvivesListenerDrop(t *testing.T) {
	pool := dbtest.NewPool(t)
	runRelay(t, pool)
	publish(t, pool, joined(id.New(), 1))
	waitFor(t, "первую доставку", 5*time.Second, func() bool { return deliveries(t, pool) == 2 })
	waitFor(t, "обрыв слушателя", 5*time.Second, func() bool {
		var killed int
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity WHERE query = 'LISTEN `+events.Channel+`' AND datname = current_database() AND pid <> pg_backend_pid()) k`).Scan(&killed)
		return killed > 0
	})
	publish(t, pool, joined(id.New(), 1))
	waitFor(t, "доставку после переподключения", 10*time.Second, func() bool { return deliveries(t, pool) == 4 })
}
