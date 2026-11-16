// Package workerhost — исполняющий воркер: проверки до базы, ожидание схемы, River с реестрами и relay outbox,
// остановка. Единственный код воркера: его собирают отдельный бинарник cmd/worker и встроенный режим cmd/api
// (API_EMBED_WORKER, спека встроенного воркера §2). slog.SetDefault здесь не вызывается — журнал задаёт бинарник.
package workerhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/identity"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/migrate"
	"wf/backend/internal/platform/queue"
	"wf/backend/locales"
)

// Base — конфиг WORKER_ и источник GeoNames, проверенные до базы; общий для сервиса и разовых команд.
type Base struct {
	Config config.Worker
	Geo    geojobs.Config
}

// Load — конфиг WORKER_ и проверки до базы: relay, источник GeoNames, его HTTP-клиент (битый
// WORKER_GEONAMES_CA_FILE — отказ старта, а не x509 на первом импорте). geoHTTP — клиент, подставленный хостом
// (тесты); nil — свой.
func Load(environ []string, geoHTTP *http.Client) (Base, error) {
	cfg, err := config.Load[config.Worker]("WORKER_", environ)
	if err != nil {
		return Base{}, err
	}
	if err := errors.Join(validateRelay(cfg.Relay), validateGeoNames(cfg.GeoNames)); err != nil {
		return Base{}, err
	}
	geoClient, err := geoNamesClient(geoHTTP, cfg.GeoNames)
	if err != nil {
		return Base{}, err
	}
	return Base{Config: cfg, Geo: geojobs.Config{BaseURL: cfg.GeoNames.BaseURL, MaxCompressed: cfg.GeoNames.MaxCompressed,
		MaxUncompressed: cfg.GeoNames.MaxUncompressed, HTTPClient: geoClient}}, nil
}

// Prepared — всё, что сервису воркера нужно до подключения к базе: Base, SMTP, кольцо identity, серверные локали.
type Prepared struct {
	base   Base
	sender *mail.SMTP
	ring   *keys.Ring
	cat    *i18n.Catalog
}

// Prepare — проверки режима сервиса до базы (разовым командам не нужны): очереди ≥ 1 (R4), почта WORKER_MAIL_*
// (предупреждения SMTP — в log), кольцо WORKER_IDENTITY_SECRET_KEYS, тексты писем.
func Prepare(b Base, environ []string, log *slog.Logger) (Prepared, error) {
	if err := validateQueues(b.Config); err != nil {
		return Prepared{}, err
	}
	mc, err := config.Load[config.Mail]("WORKER_", environ)
	if err != nil {
		return Prepared{}, err
	}
	sender, err := mail.NewSMTP(mail.SMTPConfig{Addr: mc.SMTPAddr, From: mc.From,
		Username: mc.Username, Password: mc.Password, TLS: mc.TLS, Log: log})
	if err != nil {
		return Prepared{}, err
	}
	// кольцо identity — письма с кодами (план I-2); нет ключа или битый — отказ старта
	ic, err := config.Load[config.WorkerIdentity]("WORKER_", environ)
	if err != nil {
		return Prepared{}, err
	}
	ring, err := keys.NewRing(ic.SecretKeys)
	if err != nil {
		return Prepared{}, fmt.Errorf("WORKER_IDENTITY_SECRET_KEYS: %w", err)
	}
	// тексты писем модулей — серверные локали (ключи ru = en)
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{base: b, sender: sender, ring: ring, cat: cat}, nil
}

// DB — настройки пула воркера (WORKER_DATABASE_URL и др.).
func (p Prepared) DB() config.DB { return p.base.Config.DB }

// RingIDs — имена ключей кольца identity воркера по порядку (текущий первым), не сами секреты: сверка с кольцом api
// во встроенном режиме (R6).
func (p Prepared) RingIDs() []string { return p.ring.IDs() }

// Stop — сроки остановки фона: relay и мягкая остановка River; жёсткая — всегда config.HardStop (queue.Stop).
type Stop struct {
	Relay, Soft time.Duration
}

// ServiceStop — сроки отдельного воркера (бюджет — TestWorkerShutdownBudget в cmd/worker).
var ServiceStop = Stop{Relay: config.WorkerRelayStop, Soft: config.WorkerRiverSoftStop}

// Run — фон воркера на пуле pool до отмены ctx: ожидание схемы, River (очереди, исполнители, периодика), relay
// outbox. Отмена ctx до старта River в любой фазе — nil (R11); после старта — результат queue.Stop.
func Run(ctx context.Context, p Prepared, pool *pgxpool.Pool, log *slog.Logger, stop Stop) error {
	cfg := p.base.Config
	// River и relay — только на схеме своего релиза: миграции накатывает wf-api (план C, R3)
	if err := WaitSchema(ctx, pool, cfg.DB.SchemaWait, log); err != nil {
		// отмена — штатная остановка (SIGTERM), а не ошибка: ошибка на отменённом ctx бывает и обёрнутой
		// («версия схемы: context canceled»), поэтому смотрим ctx.Err(), а не errors.Is(err, context.Canceled)
		if stopping(ctx) {
			return nil
		}
		return err
	}
	reg, err := Subscriptions()
	if err != nil {
		return err
	}
	// сценарии identity ставят письма в транзакции действия — клиентом только для вставки (исполняющий клиент
	// собирается из этого же реестра исполнителей)
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		return err
	}
	idd := identity.WorkerDeps{Pool: pool, Clock: clock.System, Secrets: p.ring, River: ins, Catalog: p.cat, Log: log}
	workers, err := allWorkers(pool, reg, p.base.Geo, log, p.sender, idd)
	if err != nil {
		return err
	}
	served := queues(cfg)
	client, err := river.NewClient(riverpgxv5.New(pool), riverConfig(served, workers, log))
	if err != nil {
		return err
	}
	queue.WarnPool(log, queue.Slots(served), cfg.DB.MaxConns)
	if stopping(ctx) {
		return nil
	}
	// Отмена ctx Start в River — жёсткая остановка (ctx задач отменяется сразу); останавливаем
	// сами: сначала relay, потом мягко River (queue.Stop).
	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	relay := events.NewRelay(pool, client, reg, log, events.RelayConfig{Batch: cfg.Relay.Batch, Poll: cfg.Relay.Poll,
		StopTimeout: stop.Relay})
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := relay.Run(ctx); err != nil {
			// завис LISTEN или пачка — остановка River не ждёт их дольше срока (бюджет, план C R4)
			log.Warn("events: relay", "err", err)
		}
	})
	log.Info("воркер запущен", "queues", cfg.Queues, "geo_import", cfg.GeoImportEnabled)
	<-ctx.Done()
	wg.Wait() // relay больше не ставит задачи (или не уложился в stop.Relay)
	// relay + мягкая River + жёсткая (queue.Stop) — в пределах config.StopBudget
	return queue.Stop(context.Background(), client, stop.Soft)
}

// stopping — ctx отменён: остановка штатная (SIGTERM), Run возвращает nil.
func stopping(ctx context.Context) bool { return ctx.Err() != nil }

// schemaPoll — как часто воркер опрашивает версию схемы при старте.
const schemaPoll = time.Second

// WaitSchema — ждать, пока wf-api не накатит миграции релиза (план C, R3): воркер — до WORKER_DB_SCHEMA_WAIT, разовая
// команда — одна проверка (timeout 0).
func WaitSchema(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration, log *slog.Logger) error {
	want, err := migrate.Latest()
	if err != nil {
		return err
	}
	return migrate.WaitSchema(ctx, pool, want, timeout, schemaPoll, clock.System, log)
}
