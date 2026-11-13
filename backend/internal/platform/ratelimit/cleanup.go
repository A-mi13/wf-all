package ratelimit

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit/ratelimitdb"
)

const cleanupBatch = 1000

// CleanupArgs — чистка строк rate_limits с tat в прошлом (спека §9.3): они ничего не ограничивают. Лимиты
// admin-api (staff_rate_limits) чистит не воркер, а тикер admin-api — DeleteExpired(…, Staff): у воркера на
// них прав нет.
type CleanupArgs struct {
	V int `json:"v"`
}

func (CleanupArgs) Kind() string { return "ratelimit.cleanup" }

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: 10 * time.Minute}}
}

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	db    ratelimitdb.DBTX
	clock clock.Clock
}

func NewCleanupWorker(db ratelimitdb.DBTX, c clock.Clock) *CleanupWorker {
	return &CleanupWorker{db: db, clock: c}
}

func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 5 * time.Minute }

// Work удаляет пачками, пока пачка полная: каждая — своя короткая транзакция.
func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	_, err := DeleteExpired(ctx, w.db, w.clock, Players)
	return err
}

// CleanupJob — каждые 10 минут и при старте воркера.
func CleanupJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: "ratelimit.cleanup", RunOnStart: true})
}
