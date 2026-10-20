package queue_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/testkit/rivertest"
)

// Списки видов бинарников: имена уникальны, у воркера и admin-api не пересекаются (вид исполняет один
// бинарник), у каждого — срок исполнителя.
func TestKinds(t *testing.T) {
	seen := map[string]bool{}
	for _, list := range [][]queue.Kind{queue.WorkerKinds(), queue.AdminKinds()} {
		for _, k := range list {
			if k.Name == "" || seen[k.Name] {
				t.Fatalf("вид %q пуст или повторяется", k.Name)
			}
			seen[k.Name] = true
		}
	}
	if !seen["mail.send"] || !seen["staff.mail"] {
		t.Fatalf("виды: %v", seen)
	}
	a := queue.WorkerKinds()
	a[0].Name = "испорчено"
	if queue.WorkerKinds()[0].Name == "испорчено" {
		t.Fatal("список общий у двух вызовов")
	}
}

// Заглушки «чужих» видов (I1 ревью 10c): рескьюер лидера River ищет исполнителя вида в своём клиенте и без него
// выбрасывает зависшую задачу (discarded). AddForeign регистрирует ровно переданные виды, со сроком настоящего
// исполнителя (рескьюер не трогает задачу, пока не вышел её срок); исполнять их заглушка не берётся.
func TestAddForeign(t *testing.T) {
	w := river.NewWorkers()
	queue.AddForeign(w, []queue.Kind{{Name: "staff.mail", Timeout: time.Minute}, {Name: "geo.import", Timeout: 30 * time.Minute}})
	if got := rivertest.Kinds(w); !slices.Equal(got, []string{"geo.import", "staff.mail"}) {
		t.Fatalf("виды %v", got)
	}
}

// Срок заглушки — срок настоящего исполнителя: рескьюер лидера не трогает задачу, пока он не вышел (иначе вернул бы
// в очередь ещё идущую задачу другого бинарника — двойное исполнение). Вид со сроком 3 ч, зависший 2 ч, остаётся
// running; контрольный вид без своего срока за то же время — retryable (рескьюер точно прошёл).
func TestForeignTimeoutRespected(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	insert := func(kind string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO river_job (kind, queue, state, args, attempt, max_attempts, attempted_at,
			scheduled_at, priority, metadata)
			VALUES ($1, 'elsewhere', 'running', '{}', 1, 10, now() - interval '2 hours', now() - interval '2 hours', 1, '{}')
			RETURNING id`, kind).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	long, control := insert("test.long"), insert("test.control")
	w := river.NewWorkers()
	queue.AddForeign(w, []queue.Kind{{Name: "test.long", Timeout: 3 * time.Hour}, {Name: "test.control"}})
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{queue.Staff: {MaxWorkers: 1}}, Workers: w,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.StopAndCancel(sctx)
	})
	state := func(id int64) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT state FROM river_job WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	deadline := time.Now().Add(90 * time.Second)
	// retryable или уже available: планировщик лидера переводит retryable по сроку повтора
	for s := state(control); s != "retryable" && s != "available"; s = state(control) {
		if time.Now().After(deadline) {
			t.Fatalf("контрольная задача: %s, рескьюер не прошёл за 90 с", state(control))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if s := state(long); s != "running" {
		t.Fatalf("задача со сроком 3 ч через 2 ч: %s, ждали running", s)
	}
}
