// Package jobs — фоновые задачи модуля geo: импорт GeoNames (спека geo §5.4). Задачу ставит
// админка (POST /v1/geo/imports, план geo 2/2) через NewImportArgs, исполняет воркер; команда
// оператора worker geo import зовёт тот же импорт синхронно (RunNow).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/internal/source"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/queue"
)

// Ошибки импорта для сборки (cmd/worker) и ручки постановки: пакет app внутренний.
var (
	ErrCountryNotFound  = app.ErrCountryNotFound
	ErrImportInProgress = app.ErrImportInProgress
)

// Config — источник GeoNames (WORKER_GEONAMES_*). HTTPClient nil — клиент по умолчанию.
type Config struct {
	BaseURL         string
	MaxCompressed   int64
	MaxUncompressed int64
	HTTPClient      *http.Client
}

// ImportArgs — задача импорта страны. StartedBy — сотрудник, поставивший импорт (журнал и
// аудит), только id. Уникальность — по Country (тег river:"unique").
type ImportArgs struct {
	V         int        `json:"v"`
	Country   string     `json:"country" river:"unique"`
	StartedBy *uuid.UUID `json:"started_by,omitempty"`
}

// NewImportArgs — аргументы текущей версии; задачу ставить только так.
func NewImportArgs(country string, startedBy *uuid.UUID) ImportArgs {
	return ImportArgs{V: 1, Country: country, StartedBy: startedBy}
}

func (ImportArgs) Kind() string { return "geo.import" }

func (ImportArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       queue.Maintenance,
		MaxAttempts: 3,
		// по стране: пока импорт страны не завершён, второй River отбросит как дубль (409
		// geo.import_in_progress у ручки); completed в списке нет — повторный импорт после
		// успешного ставится сразу, а не после чистки River
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateScheduled,
			rivertype.JobStateRunning, rivertype.JobStateRetryable,
		}},
	}
}

type importer interface {
	Import(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error)
}

// ImportWorker — исполнитель geo.import и команды оператора.
type ImportWorker struct {
	river.WorkerDefaults[ImportArgs]
	im importer
}

func NewImportWorker(pool *pgxpool.Pool, clk clock.Clock, cfg Config, log *slog.Logger) *ImportWorker {
	return &ImportWorker{im: app.NewImporter(pool, clk, source.FetchConfig{
		BaseURL: cfg.BaseURL, MaxCompressed: cfg.MaxCompressed, MaxUncompressed: cfg.MaxUncompressed,
		HTTPClient: cfg.HTTPClient,
	}, log)}
}

// Timeout — 30 минут; строку журнала, пережившую его на 10 минут, следующий запуск считает
// прерванной (app.StaleAfter).
func (*ImportWorker) Timeout(*river.Job[ImportArgs]) time.Duration { return app.ImportTimeout }

func (w *ImportWorker) Work(ctx context.Context, job *river.Job[ImportArgs]) error {
	if job.Args.V != 1 {
		// аргументы более нового релиза: повтор возьмёт воркер, который их понимает
		return fmt.Errorf("geo: версия аргументов geo.import %d не поддерживается этим воркером", job.Args.V)
	}
	_, err := w.im.Import(ctx, job.Args.Country, job.Args.StartedBy, &job.ID)
	if errors.Is(err, app.ErrCountryNotFound) {
		return river.JobCancel(err) // повтор не поможет
	}
	return err
}

// Summary — итог импорта для оператора; сверка — slug городов.
type Summary struct {
	ImportID                                                                   uuid.UUID
	PlacesUpserted, PlacesRemoved, PlacesMissing, PlacesSkipped, NamesUpserted int
	Linked, Ambiguous, NotFound                                                []string
}

// RunNow — команда оператора: импорт синхронно, без сотрудника и задачи River (§5.4).
func (w *ImportWorker) RunNow(ctx context.Context, country string) (Summary, error) {
	r, err := w.im.Import(ctx, country, nil, nil)
	s := Summary{ImportID: r.ID, PlacesUpserted: r.PlacesUpserted, PlacesRemoved: r.PlacesRemoved,
		PlacesMissing: r.PlacesMissing, PlacesSkipped: r.PlacesSkipped, NamesUpserted: r.NamesUpserted}
	for _, c := range r.Reconciled.Linked {
		s.Linked = append(s.Linked, c.Slug)
	}
	for _, c := range r.Reconciled.Ambiguous {
		s.Ambiguous = append(s.Ambiguous, c.Slug)
	}
	for _, c := range r.Reconciled.NotFound {
		s.NotFound = append(s.NotFound, c.Slug)
	}
	return s, err
}
