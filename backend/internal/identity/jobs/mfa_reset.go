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

// mfaResetBatch — сбросов 2FA в одном вызове исполнения (каждый — своя короткая транзакция).
const mfaResetBatch = 100

// errMfaResetVersion — аргументы задачи незнакомой версии (задача другого релиза).
var errMfaResetVersion = errors.New("identity: identity.mfa_reset_execute — незнакомая версия аргументов")

// errMfaResetRowsFailed — в прогоне были упавшие сбросы (каждый в журнале).
var errMfaResetRowsFailed = errors.New("identity: identity.mfa_reset_execute — часть сбросов не исполнена")

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
// worker): до batch сбросов без skip; closed — закрытых, failed — упавших (откачены, в журнале); занятые аккаунты
// пропущены (ни там, ни там).
type MfaResetExecutor interface {
	ExecuteDueMfaResets(ctx context.Context, batch int, skip []uuid.UUID) (closed int, failed []uuid.UUID, err error)
}

// MfaResetWorker — исполнитель identity.mfa_reset_execute: пачки, пока пачка выбрана целиком закрытыми и упавшими
// (пропущенный под замком аккаунт делает пачку неполной — прогон конечен; его возьмёт следующий запуск).
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

// Work — ExecuteDueMfaResets пачками; повтор задачи безопасен (каждый сброс закрывается один раз). Упавшие строки (m2
// ревью W5) — в skip до конца прогона: пачка из одних отравленных строк не заслоняет остальные, каждая строка
// выбирается в прогоне не больше раза до успеха или падения; были упавшие — ошибка в конце прогона (повтор River,
// задача видна), исполненные остаются исполненными.
func (w *MfaResetWorker) Work(ctx context.Context, job *river.Job[MfaResetExecuteArgs]) error {
	if job.Args.V != 1 {
		return fmt.Errorf("%w: v=%d", errMfaResetVersion, job.Args.V)
	}
	var skip []uuid.UUID
	for {
		n, failed, err := w.e.ExecuteDueMfaResets(ctx, mfaResetBatch, skip)
		if err != nil {
			return err
		}
		skip = append(skip, failed...)
		if n+len(failed) < mfaResetBatch {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if len(skip) > 0 {
		return fmt.Errorf("%w: %d", errMfaResetRowsFailed, len(skip))
	}
	return nil
}
