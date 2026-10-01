package events_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Спека §6.5: опубликованный outbox и inbox старше 30 дней удаляются; неопубликованное —
// никогда, даже старое (relay ещё должен его разложить).
func TestCleanupRemovesOnlyOldProcessed(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	oldPublished := publish(t, pool, joined(id.New(), 1))
	freshPublished := publish(t, pool, joined(id.New(), 1))
	oldUnpublished := publish(t, pool, joined(id.New(), 1))
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE outbox SET published_at = now() - interval '31 days' WHERE id = $1", oldPublished)
	exec("UPDATE outbox SET published_at = now() - interval '1 day' WHERE id = $1", freshPublished)
	exec("UPDATE outbox SET occurred_at = now() - interval '40 days' WHERE id = $1", oldUnpublished)
	exec("INSERT INTO event_inbox (subscriber, event_id, processed_at) VALUES ('stats.count', $1, now() - interval '31 days')", oldPublished)
	exec("INSERT INTO event_inbox (subscriber, event_id) VALUES ('stats.count', $1)", freshPublished)

	w := events.NewCleanupWorker(pool)
	if err := w.Work(ctx, &river.Job[events.CleanupArgs]{JobRow: &rivertype.JobRow{}}); err != nil {
		t.Fatal(err)
	}
	var kept, total int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE id = ANY($1)",
		[]uuid.UUID{freshPublished, oldUnpublished}).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if kept != 2 || total != 2 {
		t.Fatalf("outbox: из нужных осталось %d из 2, всего %d — старое опубликованное не удалено или задето лишнее", kept, total)
	}
	var inboxLeft int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM event_inbox").Scan(&inboxLeft); err != nil {
		t.Fatal(err)
	}
	if inboxLeft != 1 {
		t.Fatalf("inbox: осталось %d, ждали 1", inboxLeft)
	}
}

func TestCleanupJobConventions(t *testing.T) {
	opts := (events.CleanupArgs{}).InsertOpts()
	if (events.CleanupArgs{}).Kind() != "events.cleanup" || opts.Queue != queue.Maintenance || opts.UniqueOpts.ByPeriod == 0 {
		t.Fatalf("опции чистки: %+v", opts)
	}
	if events.CleanupJob() == nil {
		t.Fatal("нет периодической задачи")
	}
}
