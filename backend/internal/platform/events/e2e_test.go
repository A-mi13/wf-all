package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Спека §1 п. 4: событие из outbox доходит до двух подписчиков через relay и River, повтор
// доставки не задваивает обработку, устаревшая версия агрегата отбрасывается.
func TestEventReachesSubscribersThroughRelayAndRiver(t *testing.T) {
	pool := dbtest.NewPool(t)
	effects(t, pool)
	reg, err := events.NewRegistry(
		events.Subscription{Subscriber: "stats.count", EventType: "teams.member_joined", Delivery: events.EveryEvent, Handle: record("stats.count")},
		events.Subscription{Subscriber: "notify.roster", EventType: "teams.member_joined", Delivery: events.LatestState, Handle: record("notify.roster")},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := worker(t, pool, reg)
	completed, stop := client.Subscribe(river.EventKindJobCompleted)
	defer stop()
	waitDone := func(n int) {
		t.Helper()
		for got := 0; got < n; {
			select {
			case e := <-completed:
				if e.Job.Kind == "events.deliver" {
					got++
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("выполнено %d доставок из %d", got, n)
			}
		}
	}
	runRelayWith(t, pool, client, reg)

	agg := id.New()
	publish(t, pool, joined(agg, 2))
	waitDone(2)
	publish(t, pool, joined(agg, 1)) // пришло позже, но версия старше
	waitDone(2)
	if s, n := effectCount(t, pool, "stats.count"), effectCount(t, pool, "notify.roster"); s != 2 || n != 1 {
		t.Fatalf("эффекты: stats.count = %d (ждали 2), notify.roster = %d (ждали 1 — v1 устарела)", s, n)
	}

	// повторная доставка тех же событий через всю цепочку: relay → River → подписчик
	if _, err := pool.Exec(context.Background(), "DELETE FROM river_job WHERE kind = 'events.deliver'"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE outbox SET published_at = NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "SELECT pg_notify('"+events.Channel+"', '')"); err != nil {
		t.Fatal(err)
	}
	waitDone(4)
	if s, n := effectCount(t, pool, "stats.count"), effectCount(t, pool, "notify.roster"); s != 2 || n != 1 {
		t.Fatalf("повтор задвоил: stats.count = %d, notify.roster = %d", s, n)
	}
}

func worker(t *testing.T, pool *pgxpool.Pool, reg *events.Registry) *river.Client[pgx.Tx] {
	t.Helper()
	w := river.NewWorkers()
	events.AddWorkers(w, pool, reg)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{events.QueueEvents: {MaxWorkers: 4}},
		Workers: w,
		Logger:  discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(ctx)
	})
	return client
}

func runRelayWith(t *testing.T, pool *pgxpool.Pool, ins events.Inserter, reg *events.Registry) {
	t.Helper()
	r := events.NewRelay(pool, ins, reg, discard, events.RelayConfig{Batch: 10, Poll: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
}
