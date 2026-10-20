package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/rivertest"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/jobs"
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

// Реестр admin-api (I1 ревью 10c): настоящие исполнители — ровно queue.AdminKinds со сроками исполнителей;
// исполняющий клиент знает и виды воркера (заглушки) — объединение обоих списков (зеркально TestWorkerKinds).
func TestAdminKinds(t *testing.T) {
	d := staff.Deps{}
	if got, want := rivertest.Kinds(realWorkers(d, nil)), names(queue.AdminKinds()); !slices.Equal(got, want) {
		t.Fatalf("исполнители admin-api %v, queue.AdminKinds %v", got, want)
	}
	timeouts := map[string]time.Duration{"staff.mail": (&jobs.MailWorker{}).Timeout(nil)}
	for _, k := range queue.AdminKinds() {
		if d, ok := timeouts[k.Name]; !ok || d != k.Timeout {
			t.Errorf("%s: срок в queue.AdminKinds %s, у исполнителя %s (есть в таблице теста: %v)", k.Name, k.Timeout, d, ok)
		}
	}
	if got, want := rivertest.Kinds(allWorkers(d, nil)), names(queue.WorkerKinds(), queue.AdminKinds()); !slices.Equal(got, want) {
		t.Fatalf("исполняющий клиент admin-api знает %v, ждали %v", got, want)
	}
}

// admin-api — лидер River, воркер убит посреди письма mail.send (задача running больше часа): рескьюер admin-api
// возвращает её в retryable (исполнит воркер), а не выбрасывает как неизвестный вид.
func TestRescuerKeepsWorkerJobs(t *testing.T) {
	e := newTestEnv(t)
	var jobID int64
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO river_job (kind, queue, state, args, attempt, max_attempts,
		attempted_at, scheduled_at, priority, metadata)
		VALUES ('mail.send', 'mail', 'running', '{"v":1,"mail":"identity.code"}', 1, 10, now() - interval '2 hours',
		now() - interval '2 hours', 1, '{}') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	cancel, done := e.startServer()
	waitRescued(t, e.owner, jobID)
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

// Очереди видов admin-api (m-r2 ревью раунда 1): staff.mail ставится в очередь, которую обслуживает исполняющий
// клиент admin-api (staffQueues), и которой нет у воркера (queue.Config); очереди двух бинарников не пересекаются —
// иначе задачу одного взяла бы заглушка другого.
func TestAdminKindsQueues(t *testing.T) {
	queues := map[string]string{"staff.mail": staff.MailArgs{}.InsertOpts().Queue}
	own, worker := staffQueues(), queue.Config(config.Queues{})
	for q := range own {
		if _, clash := worker[q]; clash {
			t.Errorf("очередь %q обслуживают и admin-api, и воркер", q)
		}
	}
	for _, k := range queue.AdminKinds() {
		q, ok := queues[k.Name]
		if !ok {
			t.Errorf("%s: нет строки в таблице очередей теста", k.Name)
			continue
		}
		if _, ok := own[q]; !ok {
			t.Errorf("%s ставится в очередь %q — её не обслуживает admin-api", k.Name, q)
		}
	}
}
