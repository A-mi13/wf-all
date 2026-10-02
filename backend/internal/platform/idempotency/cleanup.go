package idempotency

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/idempotency/idempotencydb"
	"wf/backend/internal/platform/queue"
)

const cleanupBatch = 1000

// CleanupArgs — чистка ключей старше 24 часов (спека §6.4, §9.3).
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "idempotency.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q *idempotencydb.Queries
}

func NewCleanupWorker(db idempotencydb.DBTX) *CleanupWorker {
	return &CleanupWorker{q: idempotencydb.New(db)}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 10 * time.Minute }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	for {
		n, err := w.q.DeleteExpired(ctx, cleanupBatch)
		if err != nil {
			return err
		}
		if n < cleanupBatch {
			return nil
		}
	}
}

// CleanupJob — раз в час и сразу при старте воркера.
func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "idempotency.cleanup", RunOnStart: true})
}
