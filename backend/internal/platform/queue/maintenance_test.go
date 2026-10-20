package queue_test

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
)

// Непредвиденное 1 финального ревью: обслуживание River (сроки хранения задач, рескьюер, таймауты) делает лидер
// схемы — воркер или admin-api, кто успел. Настройки у обоих бинарников из одного источника, MaintenanceConfig;
// сроки — явные значения (равны умолчаниям River v0.47), чтобы смена умолчаний при подъёме River была видна.
func TestMaintenanceConfig(t *testing.T) {
	c := queue.MaintenanceConfig()
	got := map[string]time.Duration{
		"CompletedJobRetentionPeriod": c.CompletedJobRetentionPeriod,
		"CancelledJobRetentionPeriod": c.CancelledJobRetentionPeriod,
		"DiscardedJobRetentionPeriod": c.DiscardedJobRetentionPeriod,
		"RescueStuckJobsAfter":        c.RescueStuckJobsAfter,
	}
	for name, want := range map[string]time.Duration{
		"CompletedJobRetentionPeriod": 24 * time.Hour,
		"CancelledJobRetentionPeriod": 24 * time.Hour,
		"DiscardedJobRetentionPeriod": 7 * 24 * time.Hour,
		"RescueStuckJobsAfter":        time.Hour,
	} {
		if got[name] != want {
			t.Errorf("%s = %v, ждали %v", name, got[name], want)
		}
	}
	if c.Queues != nil || c.Workers != nil || c.PeriodicJobs != nil || c.Logger != nil {
		t.Fatal("в настройках обслуживания — поля бинарника")
	}
}

// MaintenanceDiff видит любое поле river.Config вне полей бинарника (очереди, исполнители, периодика, журнал),
// отличное от MaintenanceConfig; поля бинарника — не расхождение.
func TestMaintenanceDiff(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	base := queue.ClientConfig(map[string]river.QueueConfig{queue.Staff: {MaxWorkers: 1}}, river.NewWorkers(), nil, log)
	if d := queue.MaintenanceDiff(base); len(d) != 0 {
		t.Fatalf("ClientConfig расходится с MaintenanceConfig: %v", d)
	}
	if base.Queues == nil || base.Workers == nil || base.Logger != log {
		t.Fatalf("поля бинарника не заданы: %+v", base)
	}
	for name, mutate := range map[string]func(*river.Config){
		"CompletedJobRetentionPeriod": func(c *river.Config) { c.CompletedJobRetentionPeriod = time.Hour },
		"RescueStuckJobsAfter":        func(c *river.Config) { c.RescueStuckJobsAfter = 2 * time.Hour },
		"MaxAttempts":                 func(c *river.Config) { c.MaxAttempts = 3 },
		"FetchCooldown":               func(c *river.Config) { c.FetchCooldown = time.Second },
		"Schema":                      func(c *river.Config) { c.Schema = "other" },
	} {
		c := *base
		mutate(&c)
		if d := queue.MaintenanceDiff(&c); !slices.Equal(d, []string{name}) {
			t.Errorf("%s: расхождения %v", name, d)
		}
	}
	c := *base
	c.Queues, c.Workers, c.PeriodicJobs, c.Logger = nil, nil, []*river.PeriodicJob{}, nil
	if d := queue.MaintenanceDiff(&c); len(d) != 0 {
		t.Fatalf("поля бинарника посчитаны расхождением: %v", d)
	}
}
