package queue

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Заглушка: срок — как у настоящего исполнителя (рескьюер не вернёт задачу раньше её срока), исполнять чужую
// задачу отказывается ошибкой (повтор — её возьмёт свой бинарник), а не молча «выполняет».
func TestForeignWorker(t *testing.T) {
	w := &foreignWorker{timeout: 30 * time.Minute}
	job := &river.Job[foreignArgs]{JobRow: &rivertype.JobRow{ID: 1, Kind: "geo.import"}}
	if d := w.Timeout(job); d != 30*time.Minute {
		t.Fatalf("срок %s", d)
	}
	if err := w.Work(context.Background(), job); err == nil {
		t.Fatal("заглушка исполнила чужую задачу")
	}
}
