package events

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/events/eventsdb"
	"wf/backend/internal/platform/queue"
)

// cleanupBatch — строк за один DELETE: короткие транзакции не держат блокировки.
const cleanupBatch = 1000

// CleanupArgs — чистка опубликованного outbox и старого inbox (спека §6.5, §9.3).
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "events.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q *eventsdb.Queries
}

func NewCleanupWorker(pool *pgxpool.Pool) *CleanupWorker {
	return &CleanupWorker{q: eventsdb.New(pool)}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 10 * time.Minute }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	for _, del := range []func(context.Context, int32) (int64, error){w.q.DeleteOldPublished, w.q.DeleteOldInbox} {
		for {
			n, err := del(ctx, cleanupBatch)
			if err != nil {
				return err
			}
			if n < cleanupBatch {
				break
			}
		}
	}
	return nil
}

// CleanupJob — раз в час и сразу при старте воркера.
func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "events.cleanup", RunOnStart: true})
}
