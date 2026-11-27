// Command worker — фоновые задачи: пуши, пересчёты, outbox, импорт GeoNames, файлы media.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/cmd/internal/workerhost"
	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/media/reconcile"
	"wf/backend/internal/platform/blob/blobopen"
	"wf/backend/internal/platform/clock"
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
		os.Exit(exitCode(err))
	}
}

// host — что процесс берёт у машины, где запущен: часы (команда geo import — машина оператора; журнал идёт по ним со
// сдвигом к времени базы, geojobs.OperatorClock) и HTTP-клиент загрузки GeoNames (nil — по умолчанию). Параметром, а
// не переменными пакета: тест команды подставляет отстающие часы и клиент TLS-сервера httptest, а параллельные тесты
// пакета читали бы ту же переменную (ревью уборки m4, гонка данных).
type host struct {
	clock      clock.Clock
	httpClient *http.Client
}

// run без аргументов — воркер; с аргументами — разовая команда (command). Часы и клиент — настоящие.
func run(ctx context.Context, args, environ []string, out, logOut io.Writer) error {
	return runOn(ctx, host{clock: clock.System}, args, environ, out, logOut)
}

// runOn — run на машине h: проверки до базы (workerhost.Load), журнал воркера из WORKER_LOG_*, затем разовая команда
// или сервис воркера (workerhost.Prepare и Run).
func runOn(ctx context.Context, h host, args, environ []string, out, logOut io.Writer) error {
	b, err := workerhost.Load(environ, h.httpClient)
	if err != nil {
		return err
	}
	log := logx.New(logOut, b.Config.Log).With("service", "worker")
	if len(args) > 0 {
		// разовым командам почта, кольцо identity и очереди не нужны
		slog.SetDefault(log)
		pool, err := db.Open(ctx, b.Config.DB)
		if err != nil {
			return err
		}
		defer pool.Close()
		reg, err := workerhost.Subscriptions(log)
		if err != nil {
			return err
		}
		return command(ctx, args, environ, pool, reg, b.Geo, h.clock, out, log)
	}
	// почта, кольцо identity и очереди — до подключения к базе; предупреждения SMTP — в журнал воркера
	p, err := workerhost.Prepare(b, environ, log)
	if err != nil {
		return err
	}
	// сторонние библиотеки и стандартный log — через тот же логгер с маскированием ПД
	slog.SetDefault(log)
	pool, err := db.Open(ctx, p.DB())
	if err != nil {
		// SIGTERM во время подключения (холодный старт Neon) — штатная остановка, как отмена в Run (R11)
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer pool.Close()
	return workerhost.Run(ctx, p, pool, log, workerhost.ServiceStop)
}

const usage = "без аргументов — воркер; events replay --type <модуль>.<факт> --subscriber <модуль>.<имя> --since <RFC 3339>; " +
	"geo import --country <ISO 3166-1 alpha-2>; media reconcile [--fix]"

// errUsage — неверная разовая команда (неизвестная, лишние аргументы, неизвестный или недостающий флаг): код
// выхода 2 и usage в тексте ошибки, как у admin-api (geo 1 Task 11 M4).
var errUsage = errors.New("неверная команда")

func usageError(msg string) error { return fmt.Errorf("%w: %s: %s", errUsage, msg, usage) }

// exitCode — 2 для ошибки использования, 1 для остальных.
func exitCode(err error) int {
	if errors.Is(err, errUsage) {
		return 2
	}
	return 1
}

// command — разовые команды воркера: реестр подписчиков знает только воркер, импорт GeoNames
// оператор запускает со своей машины под ролью worker. Ключи хранилища (WORKER_BLOB_*) читает только media reconcile.
func command(ctx context.Context, args, environ []string, pool *pgxpool.Pool, reg *events.Registry, geo geojobs.Config,
	clk clock.Clock, out io.Writer, log *slog.Logger) error {
	var do func() error
	switch {
	case len(args) >= 2 && args[0] == "events" && args[1] == "replay":
		do = func() error { return replay(ctx, args[2:], pool, reg, out, log) }
	case len(args) >= 2 && args[0] == "geo" && args[1] == "import":
		do = func() error { return geoImport(ctx, args[2:], pool, geo, clk, out, log) }
	case len(args) >= 2 && args[0] == "media" && args[1] == "reconcile":
		do = func() error { return mediaReconcile(ctx, args[2:], environ, pool, clk, out) }
	default:
		return usageError(fmt.Sprintf("неизвестная команда %q", strings.Join(args, " ")))
	}
	// команду запускают руками: схема отстала — ошибка сразу, без ожидания
	if err := workerhost.WaitSchema(ctx, pool, 0, log); err != nil {
		return err
	}
	return do()
}

func replay(ctx context.Context, args []string, pool *pgxpool.Pool, reg *events.Registry, out io.Writer, log *slog.Logger) error {
	fs := flag.NewFlagSet("events replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	eventType := fs.String("type", "", "тип события")
	subscriber := fs.String("subscriber", "", "подписчик")
	sinceRaw := fs.String("since", "", "с какого момента, RFC 3339")
	if err := fs.Parse(args); err != nil {
		return usageError(fmt.Sprintf("events replay: %v", err))
	}
	if fs.NArg() > 0 {
		return usageError(fmt.Sprintf("events replay: лишние аргументы %q", strings.Join(fs.Args(), " ")))
	}
	if *eventType == "" || *subscriber == "" || *sinceRaw == "" {
		return usageError("events replay: нужны --type, --subscriber и --since")
	}
	since, err := time.Parse(time.RFC3339, *sinceRaw)
	if err != nil {
		return fmt.Errorf("--since: нужен RFC 3339, например 2026-10-01T00:00:00Z")
	}
	// клиент только для вставки: очереди обслуживает работающий воркер
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
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
func geoImport(ctx context.Context, args []string, pool *pgxpool.Pool, geo geojobs.Config, base clock.Clock, out io.Writer,
	log *slog.Logger) error {
	fs := flag.NewFlagSet("geo import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	country := fs.String("country", "", "страна, ISO 3166-1 alpha-2")
	if err := fs.Parse(args); err != nil {
		return usageError(fmt.Sprintf("geo import: %v", err))
	}
	if fs.NArg() > 0 {
		return usageError(fmt.Sprintf("geo import: лишние аргументы %q", strings.Join(fs.Args(), " ")))
	}
	if *country == "" {
		return usageError("geo import: нужен --country")
	}
	cc := strings.ToUpper(*country)
	// время журнала — по базе, а не по часам машины оператора (ревью G7 m9)
	clk, err := geojobs.OperatorClock(ctx, pool, base)
	if err != nil {
		return err
	}
	s, err := geojobs.NewImportWorker(pool, clk, geo, log).RunNow(ctx, cc)
	if err != nil {
		if s.ImportID != uuid.Nil {
			fmt.Fprintf(out, "импорт %s не удался, журнал geonames_imports %s\n", cc, s.ImportID)
		}
		return fmt.Errorf("geo import %s: %w", cc, err)
	}
	printGeoSummary(out, cc, s)
	return nil
}

// mediaReconcile — сверка хранилища с базой после восстановления (спека media §6.5, deploy/server/README.md): отчёт
// «строка без файла / файл без строки» (только id и ключи); --fix удаляет файлы без строк. Хранилище — WORKER_BLOB_*
// через config.LoadBlob (fs в прод-образе — отказ); none — «хранилище выключено», код 0.
func mediaReconcile(ctx context.Context, args, environ []string, pool *pgxpool.Pool, clk clock.Clock, out io.Writer) error {
	fs := flag.NewFlagSet("media reconcile", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fix := fs.Bool("fix", false, "удалить файлы без строк")
	if err := fs.Parse(args); err != nil {
		return usageError(fmt.Sprintf("media reconcile: %v", err))
	}
	if fs.NArg() > 0 {
		return usageError(fmt.Sprintf("media reconcile: лишние аргументы %q", strings.Join(fs.Args(), " ")))
	}
	bc, err := config.LoadBlob("WORKER_", environ)
	if err != nil {
		return err
	}
	opened, err := blobopen.Open(ctx, bc, clk)
	if err != nil {
		return err
	}
	if !opened.Available {
		fmt.Fprintln(out, "хранилище выключено (WORKER_BLOB_BACKEND=none): сверять нечего")
		return nil
	}
	_, err = reconcile.Run(ctx, pool, opened.Buckets, clk, *fix, out)
	return err
}

// printGeoSummary — итог импорта в вывод команды: счётчики и перечень slug по категориям сверки.
func printGeoSummary(out io.Writer, cc string, s geojobs.Summary) {
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
}
