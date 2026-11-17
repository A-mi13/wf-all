package config

import (
	"errors"
	"fmt"
)

// Проверки WORKER_* до подключения к базе — одни и те же у воркера (отдельного и встроенного в api, workerhost) и у
// стража стенда (render.yaml): значение, которое не пустит процесс, краснит тест, а не деплой (ревью infra m4).

// Validate — relay outbox: пачка меньше 1 зациклила бы Drain, неположительный опрос уронил бы time.NewTicker паникой.
func (r Relay) Validate() error {
	var errs []error
	if r.Batch < 1 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_BATCH = %d: нужно не меньше 1", r.Batch))
	}
	if r.Poll <= 0 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_POLL = %s: нужно больше нуля", r.Poll))
	}
	return errors.Join(errs...)
}

// ValidateQueues — конкурентность каждой обслуживаемой очереди ≥ 1 (R4): River отверг бы MaxWorkers < 1 в NewClient
// уже после подключения. Очередь import проверяется, только если импорт включён (GeoImportEnabled).
func (w Worker) ValidateQueues() error {
	q := w.Queues
	type check struct {
		key string
		n   int
	}
	checks := []check{
		{"WORKER_QUEUE_EVENTS", q.Events},
		{"WORKER_QUEUE_LIFECYCLE", q.Lifecycle},
		{"WORKER_QUEUE_NOTIFY", q.Notify},
		{"WORKER_QUEUE_MAIL", q.Mail},
		{"WORKER_QUEUE_STATS", q.Stats},
		{"WORKER_QUEUE_MEDIA", q.Media},
		{"WORKER_QUEUE_MAINTENANCE", q.Maintenance},
	}
	if w.GeoImportEnabled {
		checks = append(checks, check{"WORKER_QUEUE_IMPORT", q.Import})
	}
	var errs []error
	for _, c := range checks {
		if c.n < 1 {
			errs = append(errs, fmt.Errorf("%s = %d: нужно не меньше 1", c.key, c.n))
		}
	}
	return errors.Join(errs...)
}
