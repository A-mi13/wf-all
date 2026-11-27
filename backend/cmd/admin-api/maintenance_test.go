package main

import (
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/riverqueue/river"

	identityjobs "wf/backend/internal/identity/jobs"
	mediajobs "wf/backend/internal/media/jobs"
	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
)

// Непредвиденное 1 финального ревью: исполняющий клиент admin-api — от queue.MaintenanceConfig, как у воркера
// (TestWorkerRiverMaintenance): став лидером схемы, admin-api чистит и спасает задачи с теми же сроками.
func TestAdminRiverMaintenance(t *testing.T) {
	t.Parallel()
	workers := river.NewWorkers()
	c := riverConfig(workers, slog.New(slog.DiscardHandler))
	if d := queue.MaintenanceDiff(c); len(d) != 0 {
		t.Fatalf("настройки River admin-api мимо queue.MaintenanceConfig: %v", d)
	}
	if c.Workers != workers || len(c.Queues) != 1 || c.Queues[queue.Staff].MaxWorkers != staffMailWorkers ||
		len(c.PeriodicJobs) != len(periodic.Jobs())+len(identityjobs.Periodic())+len(mediajobs.Periodic()) {
		t.Fatalf("поля admin-api: %+v", c)
	}
}

// periodicIDs — ID периодических задач по алфавиту (River их не отдаёт; reflect только читает строку). ID = вид.
func periodicIDs(jobs []*river.PeriodicJob) []string {
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, reflect.ValueOf(j).Elem().FieldByName("opts").Elem().FieldByName("ID").String())
	}
	slices.Sort(out)
	return out
}

// R7 плана I-2: admin-api бывает лидером River и ставит периодику по своему списку — он равен общему (платформа +
// identity + media), как у воркера (зеркало — TestPeriodicSameInBothBinaries в cmd/internal/workerhost): иначе, пока
// лидер — admin-api, анонимизация, сбросы 2FA, чистка identity и media не ставились бы.
func TestPeriodicSameInBothBinaries(t *testing.T) {
	t.Parallel()
	c := riverConfig(river.NewWorkers(), slog.New(slog.DiscardHandler))
	want := periodicIDs(append(append(periodic.Jobs(), identityjobs.Periodic()...), mediajobs.Periodic()...))
	if got := periodicIDs(c.PeriodicJobs); !slices.Equal(got, want) || len(want) != 8 {
		t.Fatalf("периодика admin-api %v, ждали %v", got, want)
	}
}
