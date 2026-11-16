package jobs

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/queue"
)

// fakeMfaResets — due сбросов к исполнению; каждый вызов закрывает до batch. poisoned — упавшие строки: выбираются
// первыми, пока не в skip (как ORDER BY execute_at у старших).
type fakeMfaResets struct {
	due, calls int
	poisoned   []uuid.UUID
	skips      [][]uuid.UUID
}

func (f *fakeMfaResets) ExecuteDueMfaResets(_ context.Context, batch int, skip []uuid.UUID) (int, []uuid.UUID, error) {
	f.calls++
	f.skips = append(f.skips, slices.Clone(skip))
	var failed []uuid.UUID
	for _, p := range f.poisoned {
		if len(failed) < batch && !slices.Contains(skip, p) {
			failed = append(failed, p)
		}
	}
	n := min(batch-len(failed), f.due)
	f.due -= n
	return n, failed, nil
}

func mfaResetJob(v int) *river.Job[MfaResetExecuteArgs] {
	return &river.Job[MfaResetExecuteArgs]{JobRow: &rivertype.JobRow{}, Args: MfaResetExecuteArgs{V: v}}
}

// Исполнитель зовёт ExecuteDueMfaResets, пока пачка полная; вид identity.mfa_reset_execute, очередь maintenance.
func TestMfaResetWorkerDrainsBatches(t *testing.T) {
	t.Parallel()
	f := &fakeMfaResets{due: 2*mfaResetBatch + 1}
	if err := NewMfaResetWorker(f).Work(context.Background(), mfaResetJob(1)); err != nil {
		t.Fatal(err)
	}
	if f.calls != 3 || f.due != 0 {
		t.Fatalf("вызовов %d, осталось %d", f.calls, f.due)
	}
	if (MfaResetExecuteArgs{}).Kind() != "identity.mfa_reset_execute" ||
		(MfaResetExecuteArgs{}).InsertOpts().Queue != queue.Maintenance {
		t.Fatal("вид или очередь")
	}
}

// m2 ревью W5: полная пачка из упавших строк не останавливает прогон — упавшие уходят в skip до конца прогона,
// остальные исполняются; ошибка задачи — в конце (повтор River и видимость), а не на первой строке.
func TestMfaResetWorkerSkipsFailed(t *testing.T) {
	t.Parallel()
	poisoned := make([]uuid.UUID, mfaResetBatch)
	for i := range poisoned {
		poisoned[i] = uuid.New()
	}
	f := &fakeMfaResets{due: 3, poisoned: poisoned}
	err := NewMfaResetWorker(f).Work(context.Background(), mfaResetJob(1))
	if !errors.Is(err, errMfaResetRowsFailed) {
		t.Fatalf("ошибка в конце: %v", err)
	}
	if f.due != 0 || f.calls != 2 || len(f.skips[1]) != mfaResetBatch {
		t.Fatalf("осталось %d, вызовов %d, skip %d", f.due, f.calls, len(f.skips[len(f.skips)-1]))
	}
}

// Незнакомая версия аргументов — ошибка (повтор и «мёртвая» задача), а не тихий успех.
func TestMfaResetWorkerVersion(t *testing.T) {
	t.Parallel()
	f := &fakeMfaResets{due: 1}
	if err := NewMfaResetWorker(f).Work(context.Background(), mfaResetJob(2)); !errors.Is(err, errMfaResetVersion) || f.calls != 0 {
		t.Fatalf("%v, вызовов %d", err, f.calls)
	}
}
