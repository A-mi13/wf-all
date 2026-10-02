package idempotency_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash, expires_at) VALUES
		($1, 'old', '/x', 'h', now() - interval '1 second'), ($1, 'live', '/x', 'h', now() + interval '1 hour')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := idempotency.NewCleanupWorker(pools.As).Work(ctx, &river.Job[idempotency.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	var keys []string
	rows, err := pools.Owner.Query(ctx, "SELECT key FROM idempotency_keys")
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
}
