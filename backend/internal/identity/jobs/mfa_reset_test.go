package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/queue"
)

// fakeMfaResets — due сбросов к исполнению; каждый вызов закрывает до batch.
type fakeMfaResets struct {
	due, calls int
}

func (f *fakeMfaResets) ExecuteDueMfaResets(_ context.Context, batch int) (int, error) {
	f.calls++
	n := min(batch, f.due)
	f.due -= n
	return n, nil
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

// Незнакомая версия аргументов — ошибка (повтор и «мёртвая» задача), а не тихий успех.
func TestMfaResetWorkerVersion(t *testing.T) {
	t.Parallel()
	f := &fakeMfaResets{due: 1}
	if err := NewMfaResetWorker(f).Work(context.Background(), mfaResetJob(2)); !errors.Is(err, errMfaResetVersion) || f.calls != 0 {
		t.Fatalf("%v, вызовов %d", err, f.calls)
	}
}
