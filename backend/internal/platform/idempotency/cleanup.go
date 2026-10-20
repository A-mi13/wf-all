package idempotency

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/idempotency/idempotencydb"
	"wf/backend/internal/platform/queue"
)

const cleanupBatch = 1000

// CleanupArgs — чистка просроченных ключей игроков (idempotency_keys; спека §6.4, §9.3). Ключи admin-api
// (staff_idempotency_keys) чистит не воркер, а тикер admin-api — DeleteExpired(ctx, pool, Staff): у воркера
// на них прав нет.
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "idempotency.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	db idempotencydb.DBTX
}

func NewCleanupWorker(db idempotencydb.DBTX) *CleanupWorker {
	return &CleanupWorker{db: db}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 10 * time.Minute }

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	_, err := DeleteExpired(ctx, w.db, Players)
	return err
}

// CleanupJob — раз в час и сразу при старте воркера.
func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "idempotency.cleanup", RunOnStart: true})
}
