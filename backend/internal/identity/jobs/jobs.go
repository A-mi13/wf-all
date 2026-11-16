package jobs

import (
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/app"
)

// Periodic — периодика identity (R7): чистка и исполнение сброса 2FA — раз в 10 минут, анонимизация — раз в час.
// Подключают оба бинарника, которые могут стать лидером River: cmd/worker и cmd/admin-api
// (append(periodic.Jobs(), Periodic()...)); исполняет только воркер (AddWorkers), у admin-api — заглушки
// queue.AddForeign по queue.WorkerKinds. Новый срез и новые задачи на вызов: River хранит указатели и навешивает на
// них своё состояние. ID задачи = вид: по нему сверяются списки двух бинарников (TestPeriodicSameInBothBinaries).
func Periodic() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		periodicJob(10*time.Minute, func() river.JobArgs { return MaintenanceArgs{V: 1} }),
		periodicJob(time.Hour, func() river.JobArgs { return AnonymizeArgs{V: 1} }),
		periodicJob(10*time.Minute, func() river.JobArgs { return MfaResetExecuteArgs{V: 1} }),
	}
}

func periodicJob(every time.Duration, args func() river.JobArgs) *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(every),
		func() (river.JobArgs, *river.InsertOpts) { return args(), nil },
		&river.PeriodicJobOpts{ID: args().Kind(), RunOnStart: true})
}

// AddWorkers — исполнители identity у воркера: анонимизация, исполнение сброса 2FA, чистка. Сценарии — на
// app.NewMaintenance (роль worker); d.River — клиент только для вставки (письма из транзакций сценариев). Нет
// Pool, Clock, River или Log — ошибка сборки (отказ старта).
func AddWorkers(w *river.Workers, d identity.WorkerDeps) error {
	a, err := maintenanceApp(d)
	if err != nil {
		return err
	}
	river.AddWorker(w, NewAnonymizeWorker(a))
	river.AddWorker(w, NewMfaResetWorker(a))
	river.AddWorker(w, NewMaintenanceWorker(a, d.Log))
	return nil
}

// maintenanceApp — сценарии воркера на зависимостях d.
func maintenanceApp(d identity.WorkerDeps) (*app.App, error) {
	return app.NewMaintenance(d.Pool, d.Clock, d.Secrets, d.River, d.Log)
}
