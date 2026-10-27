package main

import (
	"log/slog"
	"testing"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
)

// Непредвиденное 1 финального ревью: исполняющий клиент admin-api — от queue.MaintenanceConfig, как у воркера
// (TestWorkerRiverMaintenance): став лидером схемы, admin-api чистит и спасает задачи с теми же сроками.
func TestAdminRiverMaintenance(t *testing.T) {
	workers := river.NewWorkers()
	c := riverConfig(workers, slog.New(slog.DiscardHandler))
	if d := queue.MaintenanceDiff(c); len(d) != 0 {
		t.Fatalf("настройки River admin-api мимо queue.MaintenanceConfig: %v", d)
	}
	if c.Workers != workers || len(c.Queues) != 1 || c.Queues[queue.Staff].MaxWorkers != staffMailWorkers ||
		len(c.PeriodicJobs) != len(periodic.Jobs()) {
		t.Fatalf("поля admin-api: %+v", c)
	}
}
