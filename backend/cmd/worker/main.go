// Command worker — фоновые задачи: пуши, пересчёты, outbox.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/logx"
	"wf/backend/internal/platform/queue"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Environ(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.Worker]("WORKER_", environ)
	if err != nil {
		return err
	}
	if err := validateRelay(cfg.Relay); err != nil {
		return err
	}
	log := logx.New(logOut, cfg.Log).With("service", "worker")
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	reg, err := subscriptions()
	if err != nil {
		return err
	}
	workers := queue.NewWorkers()
	events.AddWorkers(workers, pool, reg)
	client, err := queue.NewClient(pool, workers, cfg.Queues, []*river.PeriodicJob{events.CleanupJob()}, log)
	if err != nil {
		return err
	}
	// Отмена ctx Start в River — жёсткая остановка (ctx задач отменяется сразу); останавливаем
	// сами: сначала relay, потом мягко River (queue.Stop).
	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	relay := events.NewRelay(pool, client, reg, log, events.RelayConfig{Batch: cfg.Relay.Batch, Poll: cfg.Relay.Poll})
	var wg sync.WaitGroup
	wg.Go(func() { _ = relay.Run(ctx) })
	log.Info("воркер запущен", "queues", cfg.Queues)
	<-ctx.Done()
	wg.Wait() // relay больше не ставит задачи
	return queue.Stop(context.Background(), client, 30*time.Second)
}

// validateRelay — до подключения к базе: пачка меньше 1 зациклила бы Drain, неположительный
// опрос уронил бы time.NewTicker паникой.
func validateRelay(r config.Relay) error {
	var errs []error
	if r.Batch < 1 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_BATCH = %d: нужно не меньше 1", r.Batch))
	}
	if r.Poll <= 0 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_POLL = %s: нужно больше нуля", r.Poll))
	}
	return errors.Join(errs...)
}

// subscriptions — подписчики модулей на события. Пусто, пока нет модулей: каждая спека
// модуля добавляет сюда свои Subscription (internal/<модуль>/subscribers).
func subscriptions() (*events.Registry, error) {
	return events.NewRegistry()
}
