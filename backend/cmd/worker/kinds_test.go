package main

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/testkit/dbtest"
)

// Воркер — лидер River, admin-api убит посреди письма staff.mail (задача running больше часа): рескьюер
// воркера возвращает её в retryable (исполнит admin-api), а не выбрасывает как неизвестный вид.
func TestRescuerKeepsAdminJobs(t *testing.T) {
	t.Parallel()
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
