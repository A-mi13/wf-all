package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"wf/backend/internal/platform/clock"
	"wf/backend/migrations"
)

// ErrSchemaBehind — версия схемы базы ниже вшитой в бинарник и не поднялась за отведённое время.
var ErrSchemaBehind = errors.New("схема базы отстаёт от бинарника")

// BehindError — подробности ErrSchemaBehind: версия базы и нужная; Unknown — версию прочитать не дали
// (нет права SELECT на goose_db_version или нет самой таблицы), Have тогда 0.
type BehindError struct {
	Have, Want int64
	Unknown    bool
}

func (e *BehindError) Error() string {
	have := fmt.Sprint(e.Have)
	if e.Unknown {
		have = "неизвестна (нет таблицы goose_db_version или права SELECT на неё — их выдаёт migrate up)"
	}
	return fmt.Sprintf("%s: версия базы %s, бинарнику нужна %d — миграции накатывает wf-api (migrate up при старте): "+
		"дождитесь его деплоя и повторите деплой этого сервиса (Manual Deploy)", ErrSchemaBehind, have, e.Want)
}

func (e *BehindError) Unwrap() error { return ErrSchemaBehind }

// Latest — номер последней вшитой миграции.
func Latest() (int64, error) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, n := range names {
		v, err := goose.NumericComponent(n)
		if err != nil {
			return 0, fmt.Errorf("migrate: %s: %w", n, err)
		}
		latest = max(latest, v)
	}
	if latest == 0 {
		return 0, errors.New("migrate: вшитых миграций нет")
	}
	return latest, nil
}

// WaitSchema ждёт, пока версия схемы (goose_db_version) не станет ≥ want: процесс без мигратора (admin-api,
// воркер) не стартует на старой схеме, пока wf-api не накатит миграции своего релиза (план C, R3). Опрос — раз в
// poll; срок — timeout по часам c (0 — одна проверка: команды оператора не ждут). База новее — не ошибка (старый
// инстанс при откате). Нет таблицы (42P01) или права SELECT на неё (42501, первый деплой с правкой grants.sql) —
// «схема отстаёт»: опрос до срока, одна запись WARN; прочие ошибки базы и отмена ctx — возврат сразу.
func WaitSchema(ctx context.Context, db *pgxpool.Pool, want int64, timeout, poll time.Duration, c clock.Clock, log *slog.Logger) error {
	deadline := c.Now().Add(timeout)
	warned, noted := false, false
	for {
		have, code, err := schemaVersion(ctx, db)
		unknown := code != ""
		switch {
		case err != nil:
			return fmt.Errorf("migrate: версия схемы: %w", err)
		case !unknown && have >= want:
			if noted || warned {
				log.InfoContext(ctx, "схема базы догнала бинарник", "version", have, "want", want)
			}
			return nil
		case unknown && !warned:
			warned = true
			log.WarnContext(ctx, "версия схемы не читается — считаю, что схема отстаёт (таблицу и права выдаёт migrate up wf-api)",
				"sqlstate", code, "want", want, "timeout", timeout)
		case !unknown && !noted:
			noted = true
			log.InfoContext(ctx, "схема базы отстаёт, жду миграций wf-api", "version", have, "want", want, "timeout", timeout)
		default:
			log.DebugContext(ctx, "схема базы всё ещё отстаёт", "version", have, "sqlstate", code, "want", want)
		}
		if !c.Now().Before(deadline) {
			return &BehindError{Have: have, Want: want, Unknown: unknown}
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// schemaVersion — max(version_id) применённых миграций (пустая таблица — 0); unknown — SQLSTATE 42P01 или 42501:
// версия не читается, но это «схема отстаёт», а не отказ.
func schemaVersion(ctx context.Context, db *pgxpool.Pool) (v int64, unknown string, err error) {
	err = db.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&v)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "42P01" || pg.Code == "42501") {
		return 0, pg.Code, nil
	}
	return v, "", err
}
