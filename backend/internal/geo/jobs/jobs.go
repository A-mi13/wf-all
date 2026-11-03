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

	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/internal/source"
	"wf/backend/internal/platform/clock"
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

// ImportArgs — задача импорта страны (живёт в app: её ставит сценарий админки, R8). Только id сотрудника (R3).
type ImportArgs = app.ImportArgs

// NewImportArgs — аргументы текущей версии; задачу ставить только так (регистр кода нормализуется).
func NewImportArgs(country string, startedBy *uuid.UUID) ImportArgs {
	return app.NewImportArgs(country, startedBy)
}

const (
	// snoozeInProgress — через сколько повторить задачу, пока импорт страны идёт у оператора (geo 1 Task 11 M1):
	// JobSnooze не тратит попытку и не уводит задачу в discarded.
	snoozeInProgress = time.Minute
	// maxQueuedAge — задача старше (от постановки) не откладывается, а отменяется: страховка от вечного ожидания
	// чужого импорта (R8); уникальность страны освобождается, сотрудник поставит импорт заново.
	maxQueuedAge = 2 * time.Hour
)

type importer interface {
	Import(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error)
}

// ImportWorker — исполнитель geo.import и команды оператора.
type ImportWorker struct {
	river.WorkerDefaults[ImportArgs]
	im  importer
	clk clock.Clock
	log *slog.Logger
}

func NewImportWorker(pool *pgxpool.Pool, clk clock.Clock, cfg Config, log *slog.Logger) *ImportWorker {
	return &ImportWorker{im: app.NewImporter(pool, clk, source.FetchConfig{
		BaseURL: cfg.BaseURL, MaxCompressed: cfg.MaxCompressed, MaxUncompressed: cfg.MaxUncompressed,
		HTTPClient: cfg.HTTPClient,
	}, log), clk: clk, log: log}
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
	switch {
	case errors.Is(err, app.ErrCountryNotFound):
		return river.JobCancel(err) // повтор не поможет
	case errors.Is(err, app.ErrImportInProgress):
		// импорт страны идёт у оператора: ждать, а не тратить попытки (R8); граница — включительно: ровно 2 ч ещё ждём
		if age := w.clk.Now().Sub(job.CreatedAt); age > maxQueuedAge {
			w.log.Error("задача geo.import ждёт чужой импорт страны дольше срока — отменена", "job_id", job.ID,
				"country", job.Args.Country, "age", age.String(), "err", err)
			return river.JobCancel(fmt.Errorf("geo: задача %d ждёт импорт страны дольше %s: %w", job.ID, maxQueuedAge, err))
		}
		return river.JobSnooze(snoozeInProgress)
	}
	return err
}

// Summary — итог импорта для оператора; сверка — slug городов.
type Summary struct {
	ImportID                                                                   uuid.UUID
	PlacesUpserted, PlacesRemoved, PlacesMissing, PlacesSkipped, NamesUpserted int
	Linked, Ambiguous, NotFound, Conflict                                      []string
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
	for _, c := range r.Reconciled.Conflict {
		s.Conflict = append(s.Conflict, c.Slug)
	}
	return s, err
}
