package queue_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

// holdArgs — тестовая задача, которая держит воркер: hold > 0 — столько, hold == 0 — до отмены ctx.
type holdArgs struct {
	HoldMS int `json:"hold_ms"`
}

func (holdArgs) Kind() string { return "test.hold" }

func (holdArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: queue.Maintenance} }

type holdWorker struct {
	river.WorkerDefaults[holdArgs]
	started   chan struct{}
	cancelled atomic.Bool // ctx задачи отменён, пока она работала
	finished  atomic.Bool // Work вернулся
}

func (w *holdWorker) Work(ctx context.Context, job *river.Job[holdArgs]) error {
	defer w.finished.Store(true)
	close(w.started)
	var hold <-chan time.Time // nil — ждать только отмены
	if job.Args.HoldMS > 0 {
		hold = time.After(time.Duration(job.Args.HoldMS) * time.Millisecond)
	}
	select {
	case <-hold:
		return nil
	case <-ctx.Done():
		w.cancelled.Store(true)
		return ctx.Err()
	}
}

// startHolding запускает клиент на контексте без отмены (как воркер) и ждёт, пока задача
// возьмётся в работу.
func startHolding(t *testing.T, holdMS int) (*river.Client[pgx.Tx], *holdWorker) {
	t.Helper()
	pool := dbtest.NewPool(t)
	w := &holdWorker{started: make(chan struct{})}
	workers := queue.NewWorkers()
	river.AddWorker(workers, w)
	c := config.Queues{Events: 1, Lifecycle: 1, Notify: 1, Mail: 1, Stats: 1, Media: 1, Maintenance: 1}
	client, err := queue.NewClient(pool, workers, c, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.StopAndCancel(context.Background()) }) // до закрытия пула
	if _, err := client.Insert(ctx, holdArgs{HoldMS: holdMS}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.started:
	case <-time.After(15 * time.Second):
		t.Fatal("задача не взята в работу за 15 с")
	}
	return client, w
}

// Мягкая остановка (SIGTERM воркера): идущая задача доделывается, её ctx не отменяется.
func TestStopLetsRunningJobFinish(t *testing.T) {
	client, w := startHolding(t, 400)
	if err := queue.Stop(context.Background(), client, 10*time.Second); err != nil {
		t.Fatalf("мягкая остановка: %v", err)
	}
	if w.cancelled.Load() || !w.finished.Load() {
		t.Fatalf("задача: отменена = %v, завершена = %v — ждали доделанную без отмены",
			w.cancelled.Load(), w.finished.Load())
	}
}

// Задача не уложилась в мягкий срок — её ctx отменяется, Stop возвращается в пределах срока
// жёсткой остановки и сообщает, что мягкая не удалась.
func TestStopCancelsJobAfterTimeout(t *testing.T) {
	client, w := startHolding(t, 0)
	began := time.Now()
	err := queue.Stop(context.Background(), client, 200*time.Millisecond)
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("остановка заняла %s", took)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, ждали истёкший срок мягкой остановки", err)
	}
	if !w.cancelled.Load() || !w.finished.Load() {
		t.Fatalf("задача: отменена = %v, завершена = %v — ждали отменённую", w.cancelled.Load(), w.finished.Load())
	}
}
