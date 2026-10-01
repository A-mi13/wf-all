// Package queue — фоновые задачи на River (очередь в Postgres).
// Worker масштабируется отдельно от API: инстансов сколько угодно, задачи не задвоятся.
package queue

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/config"
)

// Очереди воркера (спека §9.1). Каждая задача объявляет свою в InsertOpts.
const (
	Events      = "events"      // доставка событий подписчикам
	Lifecycle   = "lifecycle"   // переходы матчей, окна протокола и голосования
	Notify      = "notify"      // пуши и письма-уведомления
	Mail        = "mail"        // транзакционные письма — отдельно от рассылок
	Stats       = "stats"       // ночные пересчёты
	Media       = "media"       // обработка загрузок
	Maintenance = "maintenance" // чистка, сроки, сверки, отложенные действия админки
)

// PingArgs — проверка живости очереди: мониторинг ставит задачу и ждёт её выполнения.
type PingArgs struct{}

func (PingArgs) Kind() string { return "platform.ping" }

func (PingArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: Maintenance} }

type PingWorker struct{ river.WorkerDefaults[PingArgs] }

func (*PingWorker) Work(context.Context, *river.Job[PingArgs]) error { return nil }

// NewWorkers — реестр воркеров платформы. Модули регистрируют свои в этом же реестре.
func NewWorkers() *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &PingWorker{})
	return w
}

// Config — очереди §9.1 с конкурентностью из конфига. Очереди default нет намеренно.
func Config(c config.Queues) map[string]river.QueueConfig {
	return map[string]river.QueueConfig{
		Events:      {MaxWorkers: c.Events},
		Lifecycle:   {MaxWorkers: c.Lifecycle},
		Notify:      {MaxWorkers: c.Notify},
		Mail:        {MaxWorkers: c.Mail},
		Stats:       {MaxWorkers: c.Stats},
		Media:       {MaxWorkers: c.Media},
		Maintenance: {MaxWorkers: c.Maintenance},
	}
}

func NewClient(pool *pgxpool.Pool, workers *river.Workers, c config.Queues,
	periodic []*river.PeriodicJob, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:       Config(c),
		Workers:      workers,
		PeriodicJobs: periodic,
		Logger:       log,
	})
}
