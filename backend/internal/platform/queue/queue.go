// Package queue — фоновые задачи на River (очередь в Postgres).
// Worker масштабируется отдельно от API: инстансов сколько угодно, задачи не задвоятся.
package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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

// hardStopTimeout — сколько ждать задачи после отмены их ctx, если мягкая остановка не уложилась.
const hardStopTimeout = 5 * time.Second

// Stop — остановка клиента, запущенного на ctx без отмены (context.WithoutCancel): отмена ctx
// Start в River — это StopAndCancel, идущие задачи потеряли бы ctx сразу. Сначала мягко —
// новые задачи не берутся, идущие доделываются в пределах timeout; не уложились — их ctx
// отменяется, и Stop возвращает ошибку мягкой остановки.
func Stop(ctx context.Context, c *river.Client[pgx.Tx], timeout time.Duration) error {
	softCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	softErr := c.Stop(softCtx)
	if softErr == nil {
		return nil
	}
	softErr = fmt.Errorf("queue: мягкая остановка не уложилась в %s, задачи отменены: %w", timeout, softErr)
	// Жёсткая — даже если ctx вызывающего уже отменён: клиент нужно остановить в любом случае.
	hardCtx, cancelHard := context.WithTimeout(context.WithoutCancel(ctx), hardStopTimeout)
	defer cancelHard()
	if err := c.StopAndCancel(hardCtx); err != nil {
		return errors.Join(softErr, fmt.Errorf("queue: жёсткая остановка: %w", err))
	}
	return softErr
}
