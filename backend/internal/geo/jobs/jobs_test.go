package jobs_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/jobs"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestImportArgsOptions(t *testing.T) {
	staff := uuid.New()
	a := jobs.NewImportArgs("RU", &staff)
	if a.V != 1 || a.Country != "RU" || a.StartedBy == nil || *a.StartedBy != staff || a.Kind() != "geo.import" {
		t.Fatalf("%+v %s", a, a.Kind())
	}
	o := a.InsertOpts()
	if o.Queue != queue.Maintenance || o.MaxAttempts != 3 || !o.UniqueOpts.ByArgs {
		t.Fatalf("%+v", o)
	}
	want := []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateScheduled,
		rivertype.JobStateRunning, rivertype.JobStateRetryable}
	if !slices.Equal(o.UniqueOpts.ByState, want) {
		t.Fatalf("ByState = %v — спека §5.4: без completed", o.UniqueOpts.ByState)
	}
	w := jobs.NewImportWorkerWith(nil)
	// go vet не пропускает got != 30*time.Minute || got != app.ImportTimeout (обе — константы):
	// сравниваем по отдельности
	got := w.Timeout(&river.Job[jobs.ImportArgs]{})
	if got != 30*time.Minute {
		t.Fatalf("Timeout = %s", got)
	}
	if got != app.ImportTimeout {
		t.Fatalf("Timeout = %s, app.ImportTimeout = %s", got, app.ImportTimeout)
	}
	// зависшая строка журнала — таймаут задачи + 10 минут (§5.4 шаг 1)
	if app.StaleAfter != app.ImportTimeout+10*time.Minute {
		t.Fatalf("StaleAfter = %s", app.StaleAfter)
	}
}

// Уникальность по стране в настоящем River: тот же код от другого сотрудника — дубль, другая
// страна — нет; после completed та же страна ставится снова.
func TestImportUniquePerCountryUntilCompleted(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	insert := func(country string, by *uuid.UUID) *rivertype.JobInsertResult {
		t.Helper()
		r, err := c.Insert(ctx, jobs.NewImportArgs(country, by), nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := insert("RU", nil)
	if first.UniqueSkippedAsDuplicate || first.Job.Queue != queue.Maintenance || first.Job.MaxAttempts != 3 || first.Job.Kind != "geo.import" {
		t.Fatalf("первая: %+v", first.Job)
	}
	staff := uuid.New()
	if !insert("RU", &staff).UniqueSkippedAsDuplicate {
		t.Fatal("вторая задача той же страны поставлена")
	}
	if insert("KZ", nil).UniqueSkippedAsDuplicate {
		t.Fatal("другая страна отброшена как дубль")
	}
	if _, err := pool.Exec(ctx, `UPDATE river_job SET state = 'completed', finalized_at = now() WHERE id = $1`, first.Job.ID); err != nil {
		t.Fatal(err)
	}
	if insert("RU", nil).UniqueSkippedAsDuplicate {
		t.Fatal("после completed та же страна не ставится")
	}
}

func TestImportWorkerWork(t *testing.T) {
	ctx := context.Background()
	staff := uuid.New()
	var gotCountry string
	var gotBy *uuid.UUID
	var gotJob *int64
	var result error
	w := jobs.NewImportWorkerWith(func(_ context.Context, country string, by *uuid.UUID, job *int64) (app.ImportResult, error) {
		gotCountry, gotBy, gotJob = country, by, job
		return app.ImportResult{}, result
	})
	job := &river.Job[jobs.ImportArgs]{JobRow: &rivertype.JobRow{ID: 42}, Args: jobs.NewImportArgs("RU", &staff)}
	if err := w.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	if gotCountry != "RU" || gotBy == nil || *gotBy != staff || gotJob == nil || *gotJob != 42 {
		t.Fatalf("импорт позван с %s %v %v", gotCountry, gotBy, gotJob)
	}
	// страны нет — отмена без повторов
	result = app.ErrCountryNotFound
	if err := w.Work(ctx, job); !errors.Is(err, app.ErrCountryNotFound) || !errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("нет страны: %v", err)
	}
	// идёт импорт оператора — обычная ошибка: River повторит
	result = app.ErrImportInProgress
	if err := w.Work(ctx, job); !errors.Is(err, jobs.ErrImportInProgress) || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("занято: %v", err)
	}
	// аргументы новой версии — обычная ошибка, импорт не зовётся
	gotCountry = ""
	v2 := &river.Job[jobs.ImportArgs]{JobRow: &rivertype.JobRow{ID: 43}, Args: jobs.ImportArgs{V: 2, Country: "RU"}}
	if err := w.Work(ctx, v2); err == nil || errors.Is(err, &river.JobCancelError{}) || gotCountry != "" {
		t.Fatalf("V=2: %v, импорт позван: %q", err, gotCountry)
	}
}

func TestImportWorkerRunNow(t *testing.T) {
	importID := uuid.New()
	w := jobs.NewImportWorkerWith(func(_ context.Context, country string, by *uuid.UUID, job *int64) (app.ImportResult, error) {
		if country != "RU" || by != nil || job != nil {
			t.Errorf("команда оператора: %s %v %v — ждали nil, nil", country, by, job)
		}
		return app.ImportResult{ID: importID, PlacesUpserted: 3, PlacesSkipped: 2, NamesUpserted: 12, Reconciled: app.Reconciled{
			Linked:    []app.LinkedCity{{Slug: "a"}},
			Ambiguous: []app.UnmatchedCity{{Slug: "b"}},
			NotFound:  []app.UnmatchedCity{{Slug: "c"}, {Slug: "d"}},
		}}, nil
	})
	s, err := w.RunNow(context.Background(), "RU")
	if err != nil {
		t.Fatal(err)
	}
	if s.ImportID != importID || s.PlacesUpserted != 3 || s.PlacesSkipped != 2 || s.NamesUpserted != 12 || !slices.Equal(s.Linked, []string{"a"}) ||
		!slices.Equal(s.Ambiguous, []string{"b"}) || !slices.Equal(s.NotFound, []string{"c", "d"}) {
		t.Fatalf("%+v", s)
	}
}
