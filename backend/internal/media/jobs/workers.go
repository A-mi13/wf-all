package jobs

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/queue"
)

// WorkerDeps — зависимости исполнителей media у воркера (сборка — cmd/internal/workerhost): пул роли worker, часы,
// реестр целей (тот же набор, что у api и admin-api, R12), открытое хранилище (Available = false — режим none: задачи
// отменяются), порт проверки содержимого (прод — media.CheckOff), журнал.
type WorkerDeps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Registry *media.Registry
	Blob     blob.Opened
	Check    media.ContentCheck
	Log      *slog.Logger
}

// AddWorkers — исполнители media.process и media.purge (очередь media) и media.cleanup (maintenance). Нет пула, часов, реестра, проверки или
// журнала, у открытого хранилища нет бакета или Purger — ошибка сборки (отказ старта).
func AddWorkers(w *river.Workers, d WorkerDeps) error {
	p, err := NewProcessWorker(d)
	if err != nil {
		return err
	}
	g, err := NewPurgeWorker(d)
	if err != nil {
		return err
	}
	c, err := NewCleanupWorker(d)
	if err != nil {
		return err
	}
	river.AddWorker(w, p)
	river.AddWorker(w, g)
	river.AddWorker(w, c)
	return nil
}

func checkBlob(b blob.Opened) error {
	if !b.Available {
		return nil
	}
	if b.Buckets.In == nil || b.Buckets.Pub == nil || b.Buckets.Priv == nil || b.Purger == nil {
		return errors.New("media/jobs: открытое хранилище без бакета или Purger")
	}
	return nil
}

// NewProcessWorker — исполнитель media.process; клиент River только для вставки media.purge — на том же пуле.
func NewProcessWorker(d WorkerDeps) (*ProcessWorker, error) {
	if d.Pool == nil || d.Clock == nil || d.Registry == nil || d.Check == nil || d.Log == nil {
		return nil, errors.New("media/jobs: исполнитель без пула, часов, реестра, проверки содержимого или журнала")
	}
	if err := checkBlob(d.Blob); err != nil {
		return nil, err
	}
	ins, err := river.NewClient(riverpgxv5.New(d.Pool), queue.InsertConfig(d.Log))
	if err != nil {
		return nil, fmt.Errorf("media/jobs: клиент River: %w", err)
	}
	return &ProcessWorker{d: d, ins: ins}, nil
}

// NewPurgeWorker — исполнитель media.purge: только хранилище.
func NewPurgeWorker(d WorkerDeps) (*PurgeWorker, error) {
	if err := checkBlob(d.Blob); err != nil {
		return nil, err
	}
	return &PurgeWorker{d: d}, nil
}

// insertClient — клиент River только для вставки (InsertTx в транзакциях исполнителя).
type insertClient = *river.Client[pgx.Tx]
