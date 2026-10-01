package queue_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Спека §9.1: семь очередей, конкурентность каждой — из конфига; очереди default нет —
// задача без очереди не должна молча повиснуть в никем не обслуживаемой default.
func TestConfigHasSpecQueues(t *testing.T) {
	c := config.Queues{Events: 1, Lifecycle: 2, Notify: 3, Mail: 4, Stats: 5, Media: 6, Maintenance: 7}
	got := queue.Config(c)
	want := map[string]int{
		queue.Events: 1, queue.Lifecycle: 2, queue.Notify: 3, queue.Mail: 4,
		queue.Stats: 5, queue.Media: 6, queue.Maintenance: 7,
	}
	if len(got) != len(want) {
		t.Fatalf("очередей %d, ждали %d: %v", len(got), len(want), got)
	}
	for name, n := range want {
		if got[name].MaxWorkers != n {
			t.Errorf("%s: MaxWorkers = %d, ждали %d", name, got[name].MaxWorkers, n)
		}
	}
}

func TestPingGoesToMaintenance(t *testing.T) {
	if q := (queue.PingArgs{}).InsertOpts().Queue; q != queue.Maintenance {
		t.Fatalf("ping в очереди %q", q)
	}
}

// Сквозная проверка: задача ставится в очередь в Postgres и выполняется воркером.
func TestPingJobIsProcessed(t *testing.T) {
	pool := dbtest.NewPool(t) // клон базы под нагрузкой небыстр — не в счёт бюджета задачи
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := config.Queues{Events: 1, Lifecycle: 1, Notify: 1, Mail: 1, Stats: 1, Media: 1, Maintenance: 2}
	client, err := queue.NewClient(pool, queue.NewWorkers(), c, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := client.Subscribe(river.EventKindJobCompleted)
	defer unsubscribe()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Stop(context.Background()) }) // до закрытия пула
	if _, err := client.Insert(ctx, queue.PingArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Job.Kind != "platform.ping" {
			t.Fatalf("kind = %s", ev.Job.Kind)
		}
	case <-ctx.Done():
		t.Fatal("задача не выполнена за 15 с")
	}
}
