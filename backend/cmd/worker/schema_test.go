package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/migrate"
	"wf/backend/internal/platform/testkit/dbtest"
)

// behindURL — база без последней миграции в goose_db_version: релиз воркера новее схемы.
func behindURL(t *testing.T) (string, *pgxpool.Pool, int64) {
	t.Helper()
	u := dbtest.NewURL(t)
	pool, err := pgxpool.New(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	want, err := migrate.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM goose_db_version WHERE version_id = $1`, want); err != nil {
		t.Fatal(err)
	}
	return u, pool, want
}

func runResult(t *testing.T, args, env []string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run(context.Background(), args, env, io.Discard, io.Discard) }()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatalf("worker %v не вернулся — ждёт схему", args)
		return nil
	}
}

// План C, R3: воркер нового релиза не стартует River и relay на отставшей схеме — ждёт WORKER_DB_SCHEMA_WAIT,
// затем код выхода 1 и понятная ошибка.
func TestWorkerWaitsSchema(t *testing.T) {
	t.Parallel()
	u, pool, _ := behindURL(t)
	err := runResult(t, nil, append(mailEnv, "WORKER_DATABASE_URL="+u, "WORKER_DB_SCHEMA_WAIT=1s"))
	if !errors.Is(err, migrate.ErrSchemaBehind) || exitCode(err) != 1 || !strings.Contains(err.Error(), "wf-api") {
		t.Fatalf("err = %v (код %d), ждали ErrSchemaBehind с подсказкой и код 1", err, exitCode(err))
	}
	// River не стартовал: лидер не выбран, периодика не поставлена
	var jobs int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("на отставшей схеме воркер поставил %d задач", jobs)
	}
}

// Разовые команды запускают руками: при отставшей схеме — ошибка сразу, без ожидания WORKER_DB_SCHEMA_WAIT.
func TestWorkerCommandNoWait(t *testing.T) {
	t.Parallel()
	u, _, _ := behindURL(t)
	env := []string{"WORKER_DATABASE_URL=" + u, "WORKER_DB_SCHEMA_WAIT=10m"}
	for _, args := range [][]string{
		{"events", "replay", "--type", "teams.member_joined", "--subscriber", "notify.roster", "--since", "2026-10-01T00:00:00Z"},
		{"geo", "import", "--country", "RU"},
	} {
		if err := runResult(t, args, env); !errors.Is(err, migrate.ErrSchemaBehind) || exitCode(err) != 1 {
			t.Fatalf("%v: err = %v, ждали ErrSchemaBehind и код 1", args, err)
		}
	}
}
