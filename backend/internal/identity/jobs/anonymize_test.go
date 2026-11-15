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

// fakeAnonymizer — очередь аккаунтов к анонимизации по purge_at: poisoned — строки, на которых анонимизация падает.
type fakeAnonymizer struct {
	due      []uuid.UUID
	poisoned map[uuid.UUID]bool
	done     map[uuid.UUID]bool
	calls    int
	batches  []int
}

func (f *fakeAnonymizer) AnonymizeDue(_ context.Context, batch int, skip []uuid.UUID) (int, []uuid.UUID, error) {
	f.batches = append(f.batches, batch)
	f.calls++
	if f.calls > 100 {
		return 0, nil, errors.New("прогон не кончается: те же строки выбираются снова")
	}
	selected := 0
	var failed []uuid.UUID
	for _, id := range f.due {
		if selected == batch {
			break
		}
		if f.done[id] || slices.Contains(skip, id) {
			continue
		}
		selected++
		if f.poisoned[id] {
			failed = append(failed, id)
			continue
		}
		f.done[id] = true
	}
	return selected, failed, nil
}

func newFake(n int, poisoned ...int) *fakeAnonymizer {
	f := &fakeAnonymizer{poisoned: map[uuid.UUID]bool{}, done: map[uuid.UUID]bool{}}
	for i := range n {
		id := uuid.New()
		f.due = append(f.due, id)
		if slices.Contains(poisoned, i) {
			f.poisoned[id] = true
		}
	}
	return f
}

func anonJob(v int) *river.Job[AnonymizeArgs] {
	return &river.Job[AnonymizeArgs]{JobRow: &rivertype.JobRow{}, Args: AnonymizeArgs{V: v}}
}

// Исполнитель зовёт AnonymizeDue пачками, пока пачка полная; очередь maintenance, вид identity.anonymize.
func TestAnonymizeWorkerDrainsBatches(t *testing.T) {
	t.Parallel()
	f := newFake(2*anonymizeBatch + 3)
	w := NewAnonymizeWorker(f)
	if err := w.Work(context.Background(), anonJob(1)); err != nil {
		t.Fatal(err)
	}
	if f.calls != 3 || f.batches[0] != anonymizeBatch || len(f.done) != 2*anonymizeBatch+3 {
		t.Fatalf("вызовов %d, пачки %v", f.calls, f.batches)
	}
	if (AnonymizeArgs{}).Kind() != "identity.anonymize" || (AnonymizeArgs{}).InsertOpts().Queue != queue.Maintenance {
		t.Fatal("вид или очередь")
	}
}

// Незнакомая версия аргументов — ошибка (повтор и «мёртвая» задача), а не тихий успех.
func TestAnonymizeWorkerVersion(t *testing.T) {
	t.Parallel()
	f := newFake(1)
	if err := NewAnonymizeWorker(f).Work(context.Background(), anonJob(2)); !errors.Is(err, errAnonymizeVersion) || f.calls != 0 {
		t.Fatalf("%v, вызовов %d", err, f.calls)
	}
}

// m8: пачка из одних отравленных строк (старших по purge_at) не останавливает прогон — упавшие id исключаются из
// следующей выборки, воркер идёт, пока выбрано столько, сколько просили; нормальные строки за ними анонимизированы.
func TestAnonymizeWorkerSkipsPoisoned(t *testing.T) {
	t.Parallel()
	poisoned := make([]int, anonymizeBatch)
	for i := range poisoned {
		poisoned[i] = i
	}
	f := newFake(anonymizeBatch+2, poisoned...)
	if err := NewAnonymizeWorker(f).Work(context.Background(), anonJob(1)); err != nil {
		t.Fatal(err)
	}
	if len(f.done) != 2 || f.calls != 2 {
		t.Fatalf("анонимизировано %d, вызовов %d", len(f.done), f.calls)
	}
}
