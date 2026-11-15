package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/queue"
)

type fakeAnonymizer struct {
	results []int
	calls   int
	batches []int
}

func (f *fakeAnonymizer) AnonymizeDue(_ context.Context, batch int) (int, error) {
	f.batches = append(f.batches, batch)
	n := f.results[f.calls]
	f.calls++
	return n, nil
}

func anonJob(v int) *river.Job[AnonymizeArgs] {
	return &river.Job[AnonymizeArgs]{JobRow: &rivertype.JobRow{}, Args: AnonymizeArgs{V: v}}
}

// Исполнитель зовёт AnonymizeDue пачками, пока пачка полная; очередь maintenance, вид identity.anonymize.
func TestAnonymizeWorkerDrainsBatches(t *testing.T) {
	t.Parallel()
	f := &fakeAnonymizer{results: []int{anonymizeBatch, anonymizeBatch, 3}}
	w := NewAnonymizeWorker(f)
	if err := w.Work(context.Background(), anonJob(1)); err != nil {
		t.Fatal(err)
	}
	if f.calls != 3 || f.batches[0] != anonymizeBatch {
		t.Fatalf("вызовов %d, пачки %v", f.calls, f.batches)
	}
	if (AnonymizeArgs{}).Kind() != "identity.anonymize" || (AnonymizeArgs{}).InsertOpts().Queue != queue.Maintenance {
		t.Fatal("вид или очередь")
	}
}

// Незнакомая версия аргументов — ошибка (повтор и «мёртвая» задача), а не тихий успех.
func TestAnonymizeWorkerVersion(t *testing.T) {
	t.Parallel()
	f := &fakeAnonymizer{results: []int{0}}
	if err := NewAnonymizeWorker(f).Work(context.Background(), anonJob(2)); !errors.Is(err, errAnonymizeVersion) || f.calls != 0 {
		t.Fatalf("%v, вызовов %d", err, f.calls)
	}
}
