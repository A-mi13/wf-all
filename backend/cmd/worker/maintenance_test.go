package main

import (
	"log/slog"
	"testing"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
)

// Непредвиденное 1 финального ревью: исполняющий клиент воркера — от queue.MaintenanceConfig, как у admin-api
// (TestAdminRiverMaintenance): лидером схемы может стать любой, обслуживание River не должно зависеть от того, кто.
func TestWorkerRiverMaintenance(t *testing.T) {
	workers := river.NewWorkers()
	c := riverConfig(config.Queues{Events: 3, Lifecycle: 1, Notify: 1, Mail: 1, Stats: 1, Media: 1, Maintenance: 1},
		workers, slog.New(slog.DiscardHandler))
	if d := queue.MaintenanceDiff(c); len(d) != 0 {
		t.Fatalf("настройки River воркера мимо queue.MaintenanceConfig: %v", d)
	}
	if c.Workers != workers || c.Queues[queue.Events].MaxWorkers != 3 || len(c.PeriodicJobs) != len(periodic.Jobs()) {
		t.Fatalf("поля воркера: %+v", c)
	}
}
