package main

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/testkit/rivertest"
)

func names(lists ...[]queue.Kind) []string {
	var out []string
	for _, l := range lists {
		for _, k := range l {
			out = append(out, k.Name)
		}
	}
	slices.Sort(out)
	return out
}

// Реестр воркера (I1 ревью 10c): настоящие исполнители — ровно queue.WorkerKinds (новый вид без строки там —
// падение здесь, а без строки admin-api не узнает его и выбросит зависшую задачу), сроки списка — сроки
// настоящих исполнителей; исполняющий клиент знает и виды admin-api (заглушки) — объединение обоих списков.
func TestWorkerKinds(t *testing.T) {
	reg, err := subscriptions()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	own := realWorkers(nil, reg, geojobs.Config{}, log, nil)
	if got, want := rivertest.Kinds(own), names(queue.WorkerKinds()); !slices.Equal(got, want) {
		t.Fatalf("исполнители воркера %v, queue.WorkerKinds %v", got, want)
	}
	timeouts := map[string]time.Duration{
		"platform.ping":       (&queue.PingWorker{}).Timeout(nil),
		"events.deliver":      (&events.DeliverWorker{}).Timeout(nil),
		"events.cleanup":      (&events.CleanupWorker{}).Timeout(nil),
		"ratelimit.cleanup":   (&ratelimit.CleanupWorker{}).Timeout(nil),
		"humancheck.cleanup":  (&humancheck.CleanupWorker{}).Timeout(nil),
		"idempotency.cleanup": (&idempotency.CleanupWorker{}).Timeout(nil),
		"geo.import":          (&geojobs.ImportWorker{}).Timeout(nil),
		"mail.send":           (&mail.SendWorker{}).Timeout(nil),
	}
	for _, k := range queue.WorkerKinds() {
		d, ok := timeouts[k.Name]
		if !ok || d != k.Timeout {
			t.Errorf("%s: срок в queue.WorkerKinds %s, у исполнителя %s (есть в таблице теста: %v)", k.Name, k.Timeout, d, ok)
		}
	}
	all := allWorkers(nil, reg, geojobs.Config{}, log, nil)
	if got, want := rivertest.Kinds(all), names(queue.WorkerKinds(), queue.AdminKinds()); !slices.Equal(got, want) {
		t.Fatalf("исполняющий клиент воркера знает %v, ждали %v", got, want)
	}
}

// Воркер — лидер River, admin-api убит посреди письма staff.mail (задача running больше часа): рескьюер
// воркера возвращает её в retryable (исполнит admin-api), а не выбрасывает как неизвестный вид.
func TestRescuerKeepsAdminJobs(t *testing.T) {
	url := dbtest.NewURL(t)
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var jobID int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO river_job (kind, queue, state, args, attempt, max_attempts,
		attempted_at, scheduled_at, priority, metadata)
		VALUES ('staff.mail', 'staff', 'running', '{"v":1,"kind":"staff.invite"}', 1, 10, now() - interval '2 hours',
		now() - interval '2 hours', 1, '{}') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, append(mailEnv, "WORKER_DATABASE_URL="+url), io.Discard, io.Discard) }()
	waitRescued(t, pool, jobID)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка: %v", err)
	}
}

// waitRescued — задача возвращена в очередь за 90 с (рескьюер: выборы лидера, задержка старта, первый проход сразу):
// retryable или уже available (планировщик лидера переводит retryable по сроку повтора — её исполнитель в этом тесте не
// запущен); discarded — провал сразу.
func waitRescued(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		var state string
		if err := pool.QueryRow(context.Background(), `SELECT state FROM river_job WHERE id = $1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "retryable" || state == "available" {
			return
		}
		if state == "discarded" || time.Now().After(deadline) {
			t.Fatalf("задача %d: %s, ждали retryable или available", id, state)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Очереди видов воркера (m-r2 ревью раунда 1): каждая задача из queue.WorkerKinds ставится в очередь, которую
// обслуживает воркер (queue.Config), и не в очередь admin-api (queue.Staff) — иначе её взяла бы заглушка чужого
// бинарника. Таблица теста — по виду на строку queue.WorkerKinds (новый вид без строки — падение).
func TestWorkerKindsQueues(t *testing.T) {
	// вид — из самих аргументов (Kind), а не строкой таблицы: перепутанная пара вида и аргументов не пройдёт (m-r5)
	queues := queuesOf(queue.PingArgs{}, events.DeliverArgs{}, events.CleanupArgs{}, ratelimit.CleanupArgs{},
		humancheck.CleanupArgs{}, idempotency.CleanupArgs{}, geojobs.ImportArgs{}, mail.SendArgs{})
	served := queue.Config(config.Queues{})
	for _, k := range queue.WorkerKinds() {
		q, ok := queues[k.Name]
		if !ok {
			t.Errorf("%s: нет строки в таблице очередей теста", k.Name)
			continue
		}
		if _, ok := served[q]; !ok || q == queue.Staff {
			t.Errorf("%s ставится в очередь %q — её не обслуживает воркер", k.Name, q)
		}
	}
}

// argsWithQueue — аргументы задачи со своей очередью (InsertOpts).
type argsWithQueue interface {
	river.JobArgs
	river.JobArgsWithInsertOpts
}

// queuesOf — очередь каждого вида по аргументам: вид — args.Kind().
func queuesOf(args ...argsWithQueue) map[string]string {
	out := make(map[string]string, len(args))
	for _, a := range args {
		out[a.Kind()] = a.InsertOpts().Queue
	}
	return out
}
