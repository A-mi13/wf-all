package humancheck

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/humancheck/humancheckdb"
	"wf/backend/internal/platform/queue"
)

const cleanupBatch = 1000

// CleanupArgs — чистка решений PoW с истёкшим сроком задачи (спека §9.3).
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "humancheck.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q     *humancheckdb.Queries
	clock clock.Clock
}

func NewCleanupWorker(db humancheckdb.DBTX, c clock.Clock) *CleanupWorker {
	return &CleanupWorker{q: humancheckdb.New(db), clock: c}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 5 * time.Minute }

// Work удаляет пачками, пока пачка полная: каждая — своя короткая транзакция.
func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	for {
		n, err := w.q.DeleteExpired(ctx, humancheckdb.DeleteExpiredParams{Now: w.clock.Now(), Batch: cleanupBatch})
		if err != nil {
			return err
		}
		if n < cleanupBatch {
			return nil
		}
	}
}

// CleanupJob — раз в час и при старте воркера.
func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "humancheck.cleanup", RunOnStart: true})
}
