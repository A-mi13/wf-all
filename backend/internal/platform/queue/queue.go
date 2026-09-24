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
)

// PingArgs — проверка живости очереди: мониторинг ставит задачу и ждёт её выполнения.
type PingArgs struct{}

func (PingArgs) Kind() string { return "platform.ping" }

type PingWorker struct{ river.WorkerDefaults[PingArgs] }

func (*PingWorker) Work(context.Context, *river.Job[PingArgs]) error { return nil }

// NewWorkers — реестр воркеров платформы. Модули регистрируют свои в этом же реестре.
func NewWorkers() *river.Workers {
	w := river.NewWorkers()
	river.AddWorker(w, &PingWorker{})
	return w
}

func NewClient(pool *pgxpool.Pool, workers *river.Workers, maxWorkers int, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: maxWorkers}},
		Workers: workers,
		Logger:  log,
	})
}
