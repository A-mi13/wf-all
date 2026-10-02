package humancheck_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO humancheck_spent (signature, expires_at) VALUES
		('old', $1), ('live', $2)`, start.Add(-time.Second), start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	w := humancheck.NewCleanupWorker(pools.As, clocktest.New(start))
	if err := w.Work(ctx, &river.Job[humancheck.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pools.Owner.QueryRow(ctx, "SELECT count(*) FROM humancheck_spent WHERE signature = 'live'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("живое удалено: %d %v", n, err)
	}
	if err := pools.Owner.QueryRow(ctx, "SELECT count(*) FROM humancheck_spent").Scan(&n); err != nil || n != 1 {
		t.Fatalf("строк %d", n)
	}
}
