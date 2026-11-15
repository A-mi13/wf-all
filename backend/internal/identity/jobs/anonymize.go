// Package jobs — задачи River модуля identity у воркера (спека identity §5.10, §3.11). Регистрацию исполнителей и
// периодики в cmd/worker и cmd/admin-api делает Task 19 плана I-2 (R7).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
)

// anonymizeBatch — аккаунтов в одной транзакции анонимизации.
const anonymizeBatch = 100

// errAnonymizeVersion — аргументы задачи незнакомой версии (задача другого релиза).
var errAnonymizeVersion = errors.New("identity: identity.anonymize — незнакомая версия аргументов")

// AnonymizeArgs — задача анонимизации аккаунтов с наступившим purge_at: без данных аккаунтов (ПД в аргументах нет).
type AnonymizeArgs struct {
	V int `json:"v"`
}

// Kind — вид задачи.
func (AnonymizeArgs) Kind() string { return "identity.anonymize" }

// InsertOpts — очередь maintenance, одна задача в час.
func (AnonymizeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

// Anonymizer — сценарий анонимизации (app.App.AnonymizeDue у сборки воркера, NewMaintenance под ролью worker): пачка
// до batch строк без skip; selected — выбрано строк, failed — упавшие (откачены, в журнале).
type Anonymizer interface {
	AnonymizeDue(ctx context.Context, batch int, skip []uuid.UUID) (selected int, failed []uuid.UUID, err error)
}

// AnonymizeWorker — исполнитель identity.anonymize: пачки до исчерпания (выбрано меньше пачки — последняя).
type AnonymizeWorker struct {
	river.WorkerDefaults[AnonymizeArgs]
	a Anonymizer
}

// NewAnonymizeWorker — исполнитель на сценарии анонимизации.
func NewAnonymizeWorker(a Anonymizer) *AnonymizeWorker { return &AnonymizeWorker{a: a} }

// Timeout — срок задачи.
func (*AnonymizeWorker) Timeout(*river.Job[AnonymizeArgs]) time.Duration { return 10 * time.Minute }

// Work — AnonymizeDue пачками; каждая пачка — своя транзакция, повтор задачи безопасен. Упавшие за прогон строки
// исключаются из следующих выборок (m8): пачка из одних отравленных строк не останавливает прогон, и он конечен —
// каждая строка выбирается в прогоне не больше раза до успеха или падения. Упавшие пробует следующий запуск.
func (w *AnonymizeWorker) Work(ctx context.Context, job *river.Job[AnonymizeArgs]) error {
	if job.Args.V != 1 {
		return fmt.Errorf("%w: v=%d", errAnonymizeVersion, job.Args.V)
	}
	var skip []uuid.UUID
	for {
		selected, failed, err := w.a.AnonymizeDue(ctx, anonymizeBatch, skip)
		if err != nil {
			return err
		}
		skip = append(skip, failed...)
		if selected < anonymizeBatch {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
