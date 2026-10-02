package dbtest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/platform/testkit/dbtest"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "")
	t.Setenv("WF_PG_PORT", "")
	c := dbtest.Config()
	if c.Host != "localhost" || c.Port != "15432" || c.User != "postgres" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestConfigFromEnvOverride(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "25432")
	t.Setenv("WF_PG_PORT", "25433")
	if got := dbtest.Config().Port; got != "25432" {
		t.Fatalf("port = %q, want WF_TEST_PG_PORT", got)
	}
}

// Порт dev-базы из deploy/dev/.env (WF_PG_PORT) — порт тестов по умолчанию:
// меняешь порт в одном месте, тесты идут туда же.
func TestConfigFallsBackToDevPort(t *testing.T) {
	t.Setenv("WF_TEST_PG_PORT", "")
	t.Setenv("WF_PG_PORT", "25433")
	if got := dbtest.Config().Port; got != "25433" {
		t.Fatalf("port = %q, want WF_PG_PORT", got)
	}
}

// Код платформы проверяется с правами прода: пул As работает под ролью, Owner — для подготовки.
func TestNewPoolsAsRunsUnderRole(t *testing.T) {
	ctx := context.Background()
	p := dbtest.NewPoolsAs(t, "api")
	var who string
	if err := p.As.QueryRow(ctx, "SELECT current_user").Scan(&who); err != nil {
		t.Fatal(err)
	}
	if who != "api" {
		t.Fatalf("current_user = %q, нужен api", who)
	}
	// служебная таблица мигратора закрыта для api grants.sql — значит, права применены
	_, err := p.As.Exec(ctx, "SELECT 1 FROM goose_db_version")
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("api читает goose_db_version: %v, нужен отказ 42501", err)
	}
	if _, err := p.Owner.Exec(ctx, "SELECT 1 FROM goose_db_version"); err != nil {
		t.Fatalf("Owner: %v", err)
	}
	// разрешённое api по grants.sql — работает
	if _, err := p.As.Exec(ctx, "SELECT 1 FROM feature_flags"); err != nil {
		t.Fatalf("api читает feature_flags: %v", err)
	}
}
