package queue_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Сквозная проверка: задача ставится в очередь в Postgres и выполняется воркером.
func TestPingJobIsProcessed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := dbtest.NewPool(t)
	client, err := queue.NewClient(pool, queue.NewWorkers(), 2, slog.New(slog.DiscardHandler))
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
