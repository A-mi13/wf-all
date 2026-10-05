// Command worker — фоновые задачи: пуши, пересчёты, outbox, импорт GeoNames.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/logx"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

// geoHTTPClient — HTTP-клиент загрузки GeoNames; nil — клиент по умолчанию. Тест команды
// подставляет клиент TLS-сервера httptest (его сертификат системе не известен).
var geoHTTPClient *http.Client

// run без аргументов — воркер; с аргументами — разовая команда (command).
func run(ctx context.Context, args, environ []string, out, logOut io.Writer) error {
	cfg, err := config.Load[config.Worker]("WORKER_", environ)
	if err != nil {
		return err
	}
	if err := errors.Join(validateRelay(cfg.Relay), validateGeoNames(cfg.GeoNames)); err != nil {
		return err
	}
	// почта нужна только воркеру, не разовым командам; проверяется до подключения к базе:
	// нет WORKER_MAIL_* или битый адрес SMTP — отказ старта
	var sender *mail.SMTP
	if len(args) == 0 {
		mc, err := config.Load[config.Mail]("WORKER_", environ)
		if err != nil {
			return err
		}
		if sender, err = mail.NewSMTP(mail.SMTPConfig{Addr: mc.SMTPAddr, From: mc.From,
			Username: mc.Username, Password: mc.Password, TLS: mc.TLS}); err != nil {
			return err
		}
	}
	log := logx.New(logOut, cfg.Log).With("service", "worker")
	// сторонние библиотеки и стандартный log — через тот же логгер с маскированием ПД
	slog.SetDefault(log)
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	reg, err := subscriptions()
	if err != nil {
		return err
	}
	geo := geojobs.Config{BaseURL: cfg.GeoNames.BaseURL, MaxCompressed: cfg.GeoNames.MaxCompressed,
		MaxUncompressed: cfg.GeoNames.MaxUncompressed, HTTPClient: geoHTTPClient}
	if len(args) > 0 {
		return command(ctx, args, pool, reg, geo, out, log)
	}
	workers := queue.NewWorkers()
	events.AddWorkers(workers, pool, reg)
	river.AddWorker(workers, ratelimit.NewCleanupWorker(pool, clock.System))
	river.AddWorker(workers, humancheck.NewCleanupWorker(pool, clock.System))
	river.AddWorker(workers, idempotency.NewCleanupWorker(pool))
	river.AddWorker(workers, geojobs.NewImportWorker(pool, clock.System, geo, log))
	mail.AddWorkers(workers, mails(), sender)
	periodic := []*river.PeriodicJob{events.CleanupJob(), ratelimit.CleanupJob(), humancheck.CleanupJob(), idempotency.CleanupJob()}
	client, err := queue.NewClient(pool, workers, cfg.Queues, periodic, log)
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

const usage = "без аргументов — воркер; events replay --type <модуль>.<факт> --subscriber <модуль>.<имя> --since <RFC 3339>; " +
	"geo import --country <ISO 3166-1 alpha-2>"

// command — разовые команды воркера: реестр подписчиков знает только воркер, импорт GeoNames
// оператор запускает со своей машины под ролью worker.
func command(ctx context.Context, args []string, pool *pgxpool.Pool, reg *events.Registry, geo geojobs.Config,
	out io.Writer, log *slog.Logger) error {
	switch {
	case len(args) >= 2 && args[0] == "events" && args[1] == "replay":
		return replay(ctx, args[2:], pool, reg, out, log)
	case len(args) >= 2 && args[0] == "geo" && args[1] == "import":
		return geoImport(ctx, args[2:], pool, geo, out, log)
	}
	return fmt.Errorf("неизвестная команда %q: %s", strings.Join(args, " "), usage)
}

func replay(ctx context.Context, args []string, pool *pgxpool.Pool, reg *events.Registry, out io.Writer, log *slog.Logger) error {
	fs := flag.NewFlagSet("events replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	eventType := fs.String("type", "", "тип события")
	subscriber := fs.String("subscriber", "", "подписчик")
	sinceRaw := fs.String("since", "", "с какого момента, RFC 3339")
	if err := fs.Parse(args); err != nil {
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
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log})
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

// geoImport — импорт GeoNames оператором (спека geo §5.4): синхронно в этом процессе, под ролью
// worker (WORKER_DATABASE_URL); задачу River не ставит. Журнал — started_by = NULL, аудит — operator.
func geoImport(ctx context.Context, args []string, pool *pgxpool.Pool, geo geojobs.Config, out io.Writer, log *slog.Logger) error {
	fs := flag.NewFlagSet("geo import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	country := fs.String("country", "", "страна, ISO 3166-1 alpha-2")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %s", err, usage)
	}
	if *country == "" {
		return fmt.Errorf("нужен --country: %s", usage)
	}
	cc := strings.ToUpper(*country)
	s, err := geojobs.NewImportWorker(pool, clock.System, geo, log).RunNow(ctx, cc)
	if err != nil {
		if s.ImportID != uuid.Nil {
			fmt.Fprintf(out, "импорт %s не удался, журнал geonames_imports %s\n", cc, s.ImportID)
		}
		return fmt.Errorf("geo import %s: %w", cc, err)
	}
	fmt.Fprintf(out, "импорт %s завершён, журнал geonames_imports %s\n", cc, s.ImportID)
	fmt.Fprintf(out, "мест: %d, удалено: %d, пропало из источника (держит город): %d, названий: %d\n",
		s.PlacesUpserted, s.PlacesRemoved, s.PlacesMissing, s.NamesUpserted)
	fmt.Fprintf(out, "пропущено (таймзона): %d\n", s.PlacesSkipped)
	fmt.Fprintf(out, "сверка городов: привязано %d, неоднозначно %d, не найдено %d, конфликт %d\n",
		len(s.Linked), len(s.Ambiguous), len(s.NotFound), len(s.Conflict))
	for _, slug := range s.Ambiguous {
		fmt.Fprintf(out, "  неоднозначно: %s\n", slug)
	}
	for _, slug := range s.NotFound {
		fmt.Fprintf(out, "  не найдено: %s\n", slug)
	}
	for _, slug := range s.Conflict {
		fmt.Fprintf(out, "  конфликт: %s\n", slug)
	}
	return nil
}

// validateGeoNames — до подключения к базе: источник только https, лимиты положительны.
func validateGeoNames(g config.GeoNames) error {
	var errs []error
	if u, err := url.Parse(g.BaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("WORKER_GEONAMES_BASE_URL = %q: нужен https://<хост>", g.BaseURL))
	}
	if g.MaxCompressed < 1 {
		errs = append(errs, fmt.Errorf("WORKER_GEONAMES_MAX_COMPRESSED = %d: нужно больше нуля", g.MaxCompressed))
	}
	if g.MaxUncompressed < 1 {
		errs = append(errs, fmt.Errorf("WORKER_GEONAMES_MAX_UNCOMPRESSED = %d: нужно больше нуля", g.MaxUncompressed))
	}
	return errors.Join(errs...)
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

// mails — виды писем модулей. Пусто, пока нет модулей: спека identity добавит письма с кодами
// (mail.Composer по id кода).
func mails() *mail.Registry {
	return mail.NewRegistry()
}
