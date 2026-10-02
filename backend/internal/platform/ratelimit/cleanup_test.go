package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Чистит воркер — под ролью worker.
func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO rate_limits (key, tat) VALUES
		('old', $1), ('live', $2)`, start.Add(-time.Minute), start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	w := ratelimit.NewCleanupWorker(pools.As, clocktest.New(start))
	if err := w.Work(ctx, &river.Job[ratelimit.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	var keys []string
	rows, err := pools.Owner.Query(ctx, "SELECT key FROM rate_limits ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "live" {
		t.Fatalf("осталось %v", keys)
	}
	if ratelimit.CleanupJob() == nil || (ratelimit.CleanupArgs{}).InsertOpts().Queue != "maintenance" {
		t.Fatal("чистка не в очереди maintenance")
	}
}

// Больше одной пачки: воркер повторяет удаление, пока не вычистит всё просроченное.
func TestCleanupManyBatches(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO rate_limits (key, tat)
		SELECT 'k' || g, $1 FROM generate_series(1, 2500) g`, start.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	w := ratelimit.NewCleanupWorker(pools.As, clocktest.New(start))
	if err := w.Work(ctx, &river.Job[ratelimit.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pools.Owner.QueryRow(ctx, "SELECT count(*) FROM rate_limits").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("осталось %d просроченных", n)
	}
}
