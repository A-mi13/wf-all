package app

// Импорт из админки (план B, Task 14; спека §6.2, R3, R7, R8, R22, R26, K6): постановка задачи River geo.import,
// её состояние и отмена «в очереди». Исполняет задачу воркер (jobs.ImportWorker → Importer.Import).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/queue"
)

const (
	// ImportKind — вид задачи River импорта GeoNames.
	ImportKind = "geo.import"

	permImportsRun  = "geo.imports.run"
	permImportsRead = "geo.imports.read"

	actionImportRequested = "geo.import_requested"
	actionImportCancelled = "geo.import_cancelled"

	// importJobLockClass — класс advisory-замка отмены задачи импорта (K6): «geoj» в ASCII; своё пространство
	// двухаргументной формы, не пересекается с countryLockClass («geor»).
	importJobLockClass int32 = 0x67656f6a
)

// ImportArgs — задача импорта страны (R3, R8; ответ пользователя Q1): только id сотрудника, поставившего импорт —
// river_job читает роль api, ПД сотрудников видит только admin-api (D3); «кто» — из аудита постановки
// geo.import_requested. Уникальность — по Country (тег river:"unique"). Поля и V не менять без новой версии.
type ImportArgs struct {
	V         int        `json:"v"`
	Country   string     `json:"country" river:"unique"`
	StartedBy *uuid.UUID `json:"started_by,omitempty"`
}

// NewImportArgs — аргументы текущей версии; задачу ставить только так. Код страны — в верхнем регистре (команда
// оператора принимает и «ru»; ручка админки пропускает только ^[A-Z]{2}$).
func NewImportArgs(country string, startedBy *uuid.UUID) ImportArgs {
	return ImportArgs{V: 1, Country: strings.ToUpper(country), StartedBy: startedBy}
}

func (ImportArgs) Kind() string { return ImportKind }

func (ImportArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		// Импорт занимает слот очереди maintenance до ImportTimeout (30 мин; geo 1 Task 11 M6) — принято: импорт
		// редкий и ручной; ожидание чужого импорта (JobSnooze) слот отпускает.
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

// StartImport — POST /v1/geo/imports одной транзакцией под Idempotency-Key вызывающего:
//  1. право geo.imports.run (конвейер проверил; здесь — защита в глубину), код страны ^[A-Z]{2}$;
//  2. страна есть (выключенная — можно, спека geo §4.1), иначе geo.ErrCountryNotFound;
//  3. свежая строка running страны (не старше StaleAfter, ревью G7 I1) — *geo.ImportInProgressError (JobID —
//     river_job_id строки; у оператора — nil); зависшая не мешает — её закроет begin воркера;
//  4. InsertTx задачи: дубль уникальности River — откат и *geo.ImportInProgressError с id существующей задачи (R26);
//  5. аудит geo.import_requested от сотрудника (объект geonames_import_job, id.Named; after {country_code, job_id};
//     без города) — в той же транзакции, что задача.
func (a *Admin) StartImport(ctx context.Context, s *auth.Staff, country string, m approvals.Meta) (int64, error) {
	if s.Decide(permImportsRun, uuid.Nil) != auth.Allow {
		return 0, errForbidden
	}
	if !countryCode.MatchString(country) {
		return 0, httpx.NewFieldError("body.country", "pattern")
	}
	if a.d.River == nil {
		return 0, errors.New("geo: постановка импорта без клиента River")
	}
	var jobID int64
	err := db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		if _, err := q.ImportCountry(ctx, country); errors.Is(err, pgx.ErrNoRows) {
			return geo.ErrCountryNotFound
		} else if err != nil {
			return err
		}
		// зависшая строка (старше StaleAfter по часам admin-api) не блокирует: её закроет begin воркера; если часы
		// воркера отстают и он строку зависшей ещё не считает — задача уйдёт в JobSnooze до её закрытия
		running, err := q.ImportRunningJob(ctx, geodb.ImportRunningJobParams{CountryCode: country,
			StaleBefore: a.d.Clock.Now().Add(-StaleAfter)})
		switch {
		case err == nil:
			return &geo.ImportInProgressError{JobID: running}
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		by := s.StaffID
		res, err := a.d.River.InsertTx(ctx, tx, NewImportArgs(country, &by), nil)
		if err != nil {
			return fmt.Errorf("geo: постановка импорта %s: %w", country, err)
		}
		if res.UniqueSkippedAsDuplicate {
			id := res.Job.ID
			return &geo.ImportInProgressError{JobID: &id}
		}
		jobID = res.Job.ID
		a.at(ctx, "import_inserted")
		return audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: s.Role, ActorLabel: s.Label, Action: actionImportRequested,
			ObjectType: importJobObject, ObjectID: importJobID(jobID),
			After: map[string]any{"country_code": country, "job_id": jobID}, IP: m.IP, UserAgent: m.UserAgent,
		})
	})
	if err != nil {
		return 0, err
	}
	return jobID, nil
}

// ImportJob — GET /v1/geo/imports/jobs/{jobId}: состояние задачи импорта по JobGet клиента River (R7, без SQL по
// river_job). River id не знает или это задача другого вида — geo.ErrImportJobNotFound.
func (a *Admin) ImportJob(ctx context.Context, s *auth.Staff, jobID int64) (geo.ImportJob, error) {
	if s.Decide(permImportsRead, uuid.Nil) != auth.Allow {
		return geo.ImportJob{}, errForbidden
	}
	if a.d.River == nil {
		return geo.ImportJob{}, errors.New("geo: состояние задачи без клиента River")
	}
	row, err := a.d.River.JobGet(ctx, jobID)
	if errors.Is(err, rivertype.ErrNotFound) {
		return geo.ImportJob{}, geo.ErrImportJobNotFound
	}
	if err != nil {
		return geo.ImportJob{}, err
	}
	if row.Kind != ImportKind {
		return geo.ImportJob{}, geo.ErrImportJobNotFound
	}
	return importJobOf(ctx, geodb.New(a.d.Pool), row)
}

// CancelImportJob — POST /v1/geo/imports/jobs/{jobId}/cancel (R22, K6) одной транзакцией под Idempotency-Key:
//  1. право geo.imports.run; advisory-замок задачи (ImportJobCancelLock) — отмены одной задачи идут по очереди;
//  2. строка River «до» (JobGetTx под замком): нет — geo.ErrImportJobNotFound; другой вид (staff.mail, периодика) —
//     geo.ErrImportJobNotFound, отмена не вызывается (вид задачи не меняется — проверка без гонки);
//  3. JobCancelTx: River на running не ошибается, а ставит metadata.cancel_attempted_at и шлёт pg_notify отмены —
//     итог не cancelled (running, completed, discarded) — geo.ErrImportJobNotCancellable, откат снимает и метку, и
//     уведомление (оно уходит только при коммите);
//  4. cancelled: «до» не cancelled и не running — отменил этот вызов: аудит geo.import_cancelled от сотрудника;
//     «до» уже cancelled (повтор другим ключом, второй сотрудник; K6) — 200 без второй записи; «до» running, а
//     итог cancelled — отменил кто-то другой (сам воркер), не этот вызов: тоже без аудита.
//
// Отмена бегущего импорта не предусмотрена (импорт сам закроет строку при отмене ctx).
func (a *Admin) CancelImportJob(ctx context.Context, s *auth.Staff, jobID int64, m approvals.Meta) (geo.ImportJob, error) {
	if s.Decide(permImportsRun, uuid.Nil) != auth.Allow {
		return geo.ImportJob{}, errForbidden
	}
	if a.d.River == nil {
		return geo.ImportJob{}, errors.New("geo: отмена задачи без клиента River")
	}
	var out geo.ImportJob
	err := db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		if err := q.ImportJobCancelLock(ctx, geodb.ImportJobCancelLockParams{Class: importJobLockClass, JobID: jobID}); err != nil {
			return fmt.Errorf("geo: замок отмены задачи %d: %w", jobID, err)
		}
		before, err := a.d.River.JobGetTx(ctx, tx, jobID)
		if errors.Is(err, rivertype.ErrNotFound) {
			return geo.ErrImportJobNotFound
		}
		if err != nil {
			return err
		}
		if before.Kind != ImportKind {
			return geo.ErrImportJobNotFound
		}
		a.at(ctx, "cancel_locked")
		after, err := a.d.River.JobCancelTx(ctx, tx, jobID)
		if errors.Is(err, rivertype.ErrNotFound) {
			return geo.ErrImportJobNotFound
		}
		if err != nil {
			return err
		}
		if after.State != rivertype.JobStateCancelled {
			return geo.ErrImportJobNotCancellable
		}
		if before.State != rivertype.JobStateCancelled && before.State != rivertype.JobStateRunning {
			var args ImportArgs
			if err := json.Unmarshal(after.EncodedArgs, &args); err != nil {
				return fmt.Errorf("geo: аргументы задачи %d: %w", jobID, err)
			}
			if err := audit.Write(ctx, audit.Entry{
				ActorStaffID: s.StaffID, ActorRole: s.Role, ActorLabel: s.Label, Action: actionImportCancelled,
				ObjectType: importJobObject, ObjectID: importJobID(jobID),
				After: map[string]any{"country_code": args.Country, "job_id": jobID}, IP: m.IP, UserAgent: m.UserAgent,
			}); err != nil {
				return err
			}
		}
		out, err = importJobOf(ctx, q, after)
		return err
	})
	return out, err
}

// importJobOf — задача River → GeoImportJob: состояние по контракту и последняя строка журнала задачи.
func importJobOf(ctx context.Context, q *geodb.Queries, row *rivertype.JobRow) (geo.ImportJob, error) {
	job := geo.ImportJob{JobID: row.ID, State: jobState(row.State), Attempt: row.Attempt, MaxAttempts: row.MaxAttempts,
		CreatedAt: row.CreatedAt}
	id, err := q.ImportLatestByJob(ctx, row.ID)
	switch {
	case err == nil:
		job.ImportID = &id
	case !errors.Is(err, pgx.ErrNoRows):
		return geo.ImportJob{}, err
	}
	return job, nil
}

// jobState — состояние River → GeoImportJob.state (R7): ждущие (available, scheduled, retryable, pending) — queued,
// discarded — failed. Неизвестное (новое в River) — queued: задача ещё не завершена и не выполняется.
func jobState(s rivertype.JobState) string {
	switch s {
	case rivertype.JobStateRunning:
		return geo.JobRunning
	case rivertype.JobStateCompleted:
		return geo.JobCompleted
	case rivertype.JobStateDiscarded:
		return geo.JobFailed
	case rivertype.JobStateCancelled:
		return geo.JobCancelled
	default:
		return geo.JobQueued
	}
}
