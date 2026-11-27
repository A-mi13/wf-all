package workerhost

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/identity"
	identityjobs "wf/backend/internal/identity/jobs"
	identitymail "wf/backend/internal/identity/mail"
	mediajobs "wf/backend/internal/media/jobs"
	mediasubs "wf/backend/internal/media/subscribers"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
)

// Subscriptions — подписчики модулей на события: реестр relay и доставки (Run) и переигровки (cmd/worker events
// replay). Каждая спека модуля добавляет сюда свои Subscription (internal/<модуль>/subscribers); log — журнал
// подписчиков (итоги — только числа).
func Subscriptions(log *slog.Logger) (*events.Registry, error) {
	// media: удаление аккаунта (спека media §3.10)
	return events.NewRegistry(mediasubs.Subscriptions(mediasubs.Deps{Log: log})...)
}

// mails — виды писем модулей: identity (коды, уведомления безопасности, удаление, сброс 2FA — план I-2, Task 19).
// Composer'ы identity — на кольце WORKER_IDENTITY_SECRET_KEYS (коды из письма) и серверных локалях.
func mails(idd identity.WorkerDeps) (*mail.Registry, error) {
	r := mail.NewRegistry()
	if err := identitymail.Register(r, identitymail.ComposerDeps{Pool: idd.Pool, Secrets: idd.Secrets, Catalog: idd.Catalog,
		Clock: idd.Clock}); err != nil {
		return nil, err
	}
	return r, nil
}

// realWorkers — исполнители воркера; их виды — ровно queue.WorkerKinds (сверка — TestWorkerKinds). idd — зависимости
// задач identity (письма, чистка, анонимизация, сброс 2FA; R7: исполнители identity — только у воркера); md — media
// (обработка, удаление файлов, чистка; хранилище none — задачи файлов отменяются, чистка идёт по строкам).
func realWorkers(pool *pgxpool.Pool, reg *events.Registry, geo geojobs.Config, log *slog.Logger, sender mail.Sender,
	idd identity.WorkerDeps, md mediajobs.WorkerDeps) (*river.Workers, error) {
	workers := queue.NewWorkers()
	events.AddWorkers(workers, pool, reg)
	river.AddWorker(workers, ratelimit.NewCleanupWorker(pool, clock.System))
	river.AddWorker(workers, humancheck.NewCleanupWorker(pool, clock.System))
	river.AddWorker(workers, idempotency.NewCleanupWorker(pool))
	// вид geo.import регистрируется всегда, и при выключенном импорте: очереди import тогда нет (queues), задача ждёт
	river.AddWorker(workers, geojobs.NewImportWorker(pool, clock.System, geo, log))
	letters, err := mails(idd)
	if err != nil {
		return nil, err
	}
	mail.AddWorkers(workers, letters, sender)
	if err := identityjobs.AddWorkers(workers, idd); err != nil {
		return nil, err
	}
	if err := mediajobs.AddWorkers(workers, md); err != nil {
		return nil, err
	}
	return workers, nil
}

// allWorkers — реестр исполняющего клиента воркера: свои исполнители и заглушки видов admin-api (I1 ревью 10c): став
// лидером River, рескьюер воркера вернёт зависшее письмо staff.mail в retryable, а не выбросит неизвестный вид.
func allWorkers(pool *pgxpool.Pool, reg *events.Registry, geo geojobs.Config, log *slog.Logger, sender mail.Sender,
	idd identity.WorkerDeps, md mediajobs.WorkerDeps) (*river.Workers, error) {
	workers, err := realWorkers(pool, reg, geo, log, sender, idd, md)
	if err != nil {
		return nil, err
	}
	queue.AddForeign(workers, queue.AdminKinds())
	return workers, nil
}

// riverConfig — исполняющий клиент воркера на очередях served (queues): обслуживание — общее с admin-api
// (queue.ClientConfig от queue.MaintenanceConfig, страж TestWorkerRiverMaintenance), периодика — платформа, identity и
// media:
// тот же список у admin-api, лидером River может стать любой (P27, R7; страж TestPeriodicSameInBothBinaries).
func riverConfig(served map[string]river.QueueConfig, workers *river.Workers, log *slog.Logger) *river.Config {
	return queue.ClientConfig(served, workers, periodicJobs(), log)
}

// periodicJobs — периодика воркера: платформа, identity, media (тот же список у admin-api).
func periodicJobs() []*river.PeriodicJob {
	return append(append(periodic.Jobs(), identityjobs.Periodic()...), mediajobs.Periodic()...)
}
