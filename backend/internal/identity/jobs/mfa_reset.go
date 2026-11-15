package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
)

// mfaResetBatch — сбросов 2FA в одном вызове исполнения (каждый — своя короткая транзакция).
const mfaResetBatch = 100

// errMfaResetVersion — аргументы задачи незнакомой версии (задача другого релиза).
var errMfaResetVersion = errors.New("identity: identity.mfa_reset_execute — незнакомая версия аргументов")

// MfaResetExecuteArgs — задача исполнения сбросов 2FA с наступившим execute_at (спека §3.15 п. 5): без данных
// аккаунтов (ПД в аргументах нет). Периодику и регистрацию исполнителя в cmd/worker делает Task 19 плана I-2 (R7).
type MfaResetExecuteArgs struct {
	V int `json:"v"`
}

// Kind — вид задачи.
func (MfaResetExecuteArgs) Kind() string { return "identity.mfa_reset_execute" }

// InsertOpts — очередь maintenance, одна задача в час.
func (MfaResetExecuteArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
}

// MfaResetExecutor — сценарий исполнения (app.App.ExecuteDueMfaResets у сборки воркера, NewMaintenance под ролью
// worker): закрывает до batch сбросов, возвращает число закрытых (занятые аккаунты пропущены).
type MfaResetExecutor interface {
	ExecuteDueMfaResets(ctx context.Context, batch int) (int, error)
}

// MfaResetWorker — исполнитель identity.mfa_reset_execute: пачки, пока закрыта полная пачка (пропущенный под замком
// аккаунт делает пачку неполной — прогон конечен; его возьмёт следующий запуск).
type MfaResetWorker struct {
	river.WorkerDefaults[MfaResetExecuteArgs]
	e MfaResetExecutor
}

// NewMfaResetWorker — исполнитель на сценарии исполнения сбросов.
func NewMfaResetWorker(e MfaResetExecutor) *MfaResetWorker { return &MfaResetWorker{e: e} }

// Timeout — срок задачи.
func (*MfaResetWorker) Timeout(*river.Job[MfaResetExecuteArgs]) time.Duration {
	return 10 * time.Minute
}

// Work — ExecuteDueMfaResets пачками; повтор задачи безопасен (каждый сброс закрывается один раз).
func (w *MfaResetWorker) Work(ctx context.Context, job *river.Job[MfaResetExecuteArgs]) error {
	if job.Args.V != 1 {
		return fmt.Errorf("%w: v=%d", errMfaResetVersion, job.Args.V)
	}
	for {
		n, err := w.e.ExecuteDueMfaResets(ctx, mfaResetBatch)
		if err != nil {
			return err
		}
		if n < mfaResetBatch {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
