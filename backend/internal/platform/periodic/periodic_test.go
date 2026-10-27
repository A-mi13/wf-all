package periodic_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Список — четыре чистки платформы; каждый вызов — новый срез и новые задачи: River хранит указатели
// и навешивает на них свои id, общий срез двух клиентов (воркер и admin-api в одном тесте или процессе)
// делил бы состояние.
func TestJobs(t *testing.T) {
	a, b := periodic.Jobs(), periodic.Jobs()
	if len(a) != 4 || len(b) != 4 {
		t.Fatalf("задач %d и %d, ждали 4", len(a), len(b))
	}
	for i := range a {
		if a[i] == nil || a[i] == b[i] {
			t.Fatalf("задача %d: %p и %p — ждали новую на каждый вызов", i, a[i], b[i])
		}
	}
	a[0] = nil
	if b[0] == nil {
		t.Fatal("срез общий у двух вызовов")
	}
}

// Лидер River ставит периодику по своему списку (River v0.47: «Running periodic jobs requires that the
// client be electable as leader»). admin-api — клиент без исполнителей платформы и на одной очереди staff:
// став лидером, он обязан ставить чистки воркера, а не молча оставить их без лидера. River не проверяет
// исполнителя у периодической вставки; права роли admin на river_* даёт grants.sql. Ставятся ровно задачи
// списка (RunOnStart) в очереди воркера — исполнит воркер.
func TestLeaderWithoutWorkersEnqueuesPlatformJobs(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workers := river.NewWorkers()
	river.AddWorker(workers, river.WorkFunc(func(context.Context, *river.Job[stubArgs]) error { return nil }))
	client, err := river.NewClient(riverpgxv5.New(pools.As), &river.Config{
		Queues:       map[string]river.QueueConfig{queue.Staff: {MaxWorkers: 1}},
		Workers:      workers,
		PeriodicJobs: periodic.Jobs(),
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Start — на ctx без срока: отмена ctx Start в River — жёсткая остановка в момент выхода из теста
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // до закрытия пула
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer scancel()
		_ = client.StopAndCancel(sctx)
	})
	want := []string{"events.cleanup", "humancheck.cleanup", "idempotency.cleanup", "ratelimit.cleanup"}
	for {
		rows, err := pools.Owner.Query(ctx, `SELECT kind, queue FROM river_job ORDER BY kind`)
		if err != nil {
			t.Fatal(err)
		}
		var kinds []string
		for rows.Next() {
			var kind, q string
			if err := rows.Scan(&kind, &q); err != nil {
				t.Fatal(err)
			}
			if q != queue.Maintenance {
				t.Errorf("%s в очереди %q, ждали %q (исполняет воркер)", kind, q, queue.Maintenance)
			}
			kinds = append(kinds, kind)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if slices.Equal(kinds, want) {
			return
		}
		if len(kinds) > len(want) {
			t.Fatalf("задачи %v, ждали %v", kinds, want)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("лидер без исполнителей поставил %v за 30 с, ждали %v", kinds, want)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// stubArgs — исполнитель-заглушка: River не стартует клиент с очередями без единого исполнителя.
type stubArgs struct{}

func (stubArgs) Kind() string { return "test.stub" }
