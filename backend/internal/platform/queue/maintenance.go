package queue

import (
	"log/slog"
	"reflect"
	"time"

	"github.com/riverqueue/river"
)

// Сроки обслуживания River (равны умолчаниям v0.47–v0.49, сверено 27.10.2026 — заданы явно, чтобы смену
// умолчаний при подъёме River было видно в диффе, а не в поведении).
const (
	completedRetention = 24 * time.Hour     // выполненные задачи
	cancelledRetention = 24 * time.Hour     // отменённые
	discardedRetention = 7 * 24 * time.Hour // «мёртвые»: видны и переигрываются неделю
	rescueAfter        = time.Hour          // running дольше — рескьюер возвращает в retryable (или выбрасывает без исполнителя)
)

// binaryFields — поля river.Config, которые у каждого бинарника свои: очереди, исполнители, периодика, журнал.
// Остальное — обслуживание и поведение клиента: у воркера и admin-api одинаково (MaintenanceDiff).
var binaryFields = map[string]bool{"Queues": true, "Workers": true, "PeriodicJobs": true, "Logger": true}

// MaintenanceConfig — общая часть river.Config исполняющих клиентов воркера и admin-api (непредвиденное 1
// финального ревью): обслуживание (чистка задач по срокам, рескьюер) делает лидер схемы — тот бинарник, кто
// успел, — поэтому настройки у обоих должны совпадать, иначе поведение зависит от того, кто лидер. Поля
// бинарника (binaryFields) пусты — их задаёт ClientConfig.
func MaintenanceConfig() river.Config {
	return river.Config{
		CompletedJobRetentionPeriod: completedRetention,
		CancelledJobRetentionPeriod: cancelledRetention,
		DiscardedJobRetentionPeriod: discardedRetention,
		RescueStuckJobsAfter:        rescueAfter,
	}
}

// ClientConfig — конфигурация исполняющего клиента бинарника: MaintenanceConfig и свои очереди, исполнители,
// периодика и журнал. Воркер и admin-api строят клиент только так (стражи TestWorkerRiverMaintenance,
// TestAdminRiverMaintenance).
func ClientConfig(queues map[string]river.QueueConfig, workers *river.Workers, periodic []*river.PeriodicJob, log *slog.Logger) *river.Config {
	c := MaintenanceConfig()
	c.Queues, c.Workers, c.PeriodicJobs, c.Logger = queues, workers, periodic, log
	return &c
}

// MaintenanceDiff — имена полей c вне binaryFields, отличных от MaintenanceConfig (пусто — совпадает). Для
// стражей бинарников: любое поле river.Config, заданное в одном бинарнике мимо MaintenanceConfig, — расхождение,
// в том числе поле, которое появится в новой версии River.
func MaintenanceDiff(c *river.Config) []string {
	want := MaintenanceConfig()
	got, exp := reflect.ValueOf(c).Elem(), reflect.ValueOf(&want).Elem()
	var diff []string
	for i := range got.NumField() {
		f := got.Type().Field(i)
		if !f.IsExported() || binaryFields[f.Name] {
			continue
		}
		if !reflect.DeepEqual(got.Field(i).Interface(), exp.Field(i).Interface()) {
			diff = append(diff, f.Name)
		}
	}
	return diff
}
