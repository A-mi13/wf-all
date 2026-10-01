// Command worker — фоновые задачи: пуши, пересчёты, outbox.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/logx"
	"wf/backend/internal/platform/queue"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

// run без аргументов — воркер; с аргументами — разовая команда (command).
func run(ctx context.Context, args, environ []string, out, logOut io.Writer) error {
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
	if len(args) > 0 {
		return command(ctx, args, pool, reg, out)
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
	return queue.Stop(context.Background(), client, softStopTimeout)
}

// softStopTimeout — мягкая остановка River; вместе с жёсткой (5 с в queue.Stop) укладывается в
// 30 с между SIGTERM и SIGKILL (Render).
const softStopTimeout = 25 * time.Second

const usage = "без аргументов — воркер; events replay --type <модуль>.<факт> --subscriber <модуль>.<имя> --since <RFC 3339>"

// command — разовые команды воркера: им нужен реестр подписчиков, который знает только воркер.
func command(ctx context.Context, args []string, pool *pgxpool.Pool, reg *events.Registry, out io.Writer) error {
	if len(args) < 2 || args[0] != "events" || args[1] != "replay" {
		return fmt.Errorf("неизвестная команда %q: %s", strings.Join(args, " "), usage)
	}
	fs := flag.NewFlagSet("events replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	eventType := fs.String("type", "", "тип события")
	subscriber := fs.String("subscriber", "", "подписчик")
	sinceRaw := fs.String("since", "", "с какого момента, RFC 3339")
	if err := fs.Parse(args[2:]); err != nil {
		return fmt.Errorf("%w: %s", err, usage)
	}
	if *eventType == "" || *subscriber == "" || *sinceRaw == "" {
		return fmt.Errorf("нужны --type, --subscriber и --since: %s", usage)
	}
	since, err := time.Parse(time.RFC3339, *sinceRaw)
	if err != nil {
		return fmt.Errorf("--since: нужен RFC 3339, например 2026-10-01T00:00:00Z")
	}
	// клиент только для вставки: очереди обслуживает работающий воркер
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return err
	}
	n, err := events.Replay(ctx, pool, ins, reg, *eventType, *subscriber, since)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "поставлено задач доставки: %d\n", n)
	return nil
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
