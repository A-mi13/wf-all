package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/queue"
)

// errMaintenanceVersion — аргументы задачи незнакомой версии (задача другого релиза).
var errMaintenanceVersion = errors.New("identity: identity.maintenance — незнакомая версия аргументов")

// MaintenanceArgs — чистка identity по срокам (спека §5.10): без данных аккаунтов.
type MaintenanceArgs struct {
	V int `json:"v"`
}

// Kind — вид задачи.
func (MaintenanceArgs) Kind() string { return "identity.maintenance" }

// InsertOpts — очередь maintenance, одна задача в 10 минут (как периодика).
func (MaintenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: 10 * time.Minute}}
}

// Cleaner — сценарий чистки (app.App.Cleanup у сборки воркера, NewMaintenance под ролью worker).
type Cleaner interface {
	Cleanup(ctx context.Context) (app.CleanupReport, error)
}

// MaintenanceWorker — исполнитель identity.maintenance.
type MaintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]
	c   Cleaner
	log *slog.Logger
}

// NewMaintenanceWorker — исполнитель на сценарии чистки; log — журнал воркера (итог — только числа).
func NewMaintenanceWorker(c Cleaner, log *slog.Logger) *MaintenanceWorker {
	return &MaintenanceWorker{c: c, log: log}
}

// Timeout — срок задачи.
func (*MaintenanceWorker) Timeout(*river.Job[MaintenanceArgs]) time.Duration { return 10 * time.Minute }

// Work — Cleanup; ошибка (в том числе упавшие аккаунты) — повтор River, вычищенное закоммичено пачками.
func (w *MaintenanceWorker) Work(ctx context.Context, job *river.Job[MaintenanceArgs]) error {
	if job.Args.V != 1 {
		return fmt.Errorf("%w: v=%d", errMaintenanceVersion, job.Args.V)
	}
	rep, err := w.c.Cleanup(ctx)
	if w.log != nil {
		args := make([]any, 0, 2*len(rep.Rows))
		for k, n := range rep.Rows {
			if n > 0 {
				args = append(args, k, n)
			}
		}
		if len(args) > 0 {
			w.log.InfoContext(ctx, "identity: чистка", args...)
		}
	}
	return err
}
