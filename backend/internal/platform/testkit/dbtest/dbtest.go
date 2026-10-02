// Package dbtest выдаёт каждому тесту свою чистую базу — клон мигрированного шаблона.
// pgtestdb мигрирует шаблон один раз на весь прогон (advisory-lock), клон ~10 мс.
package dbtest

import (
	"context"
	"database/sql"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"

	"wf/backend/internal/platform/grants"
	"wf/backend/migrations"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Config — подключение суперпользователем: тестам нужно CREATE DATABASE.
// Порт: WF_TEST_PG_PORT → WF_PG_PORT (порт dev-базы из deploy/dev/.env, его подставляет
// корневой Taskfile) → 15432.
func Config() pgtestdb.Config {
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       env("WF_TEST_PG_HOST", "localhost"),
		Port:       env("WF_TEST_PG_PORT", env("WF_PG_PORT", "15432")),
		User:       env("WF_TEST_PG_USER", "postgres"),
		Password:   env("WF_TEST_PG_PASSWORD", "postgres"),
		Database:   "postgres",
		Options:    "sslmode=disable",
		TestRole: &pgtestdb.Role{
			Username:     pgtestdb.DefaultRoleUsername,
			Password:     pgtestdb.DefaultRolePassword,
			Capabilities: "SUPERUSER",
		},
	}
}

func migrator() pgtestdb.Migrator {
	return goosemigrator.New(".", goosemigrator.WithFS(migrations.FS))
}

// requireServer падает с понятным советом, если Postgres не запущен.
func requireServer(t testing.TB, c pgtestdb.Config) {
	t.Helper()
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "tcp", net.JoinHostPort(c.Host, c.Port))
	if err != nil {
		t.Fatalf("Postgres недоступен на %s (порт: WF_TEST_PG_PORT → WF_PG_PORT → 15432) — "+
			"запусти ./task infra:up или проверь, что порт совпадает с WF_PG_PORT в deploy/dev/.env (%v)",
			net.JoinHostPort(c.Host, c.Port), err)
	}
	_ = conn.Close()
}

func New(t testing.TB) *sql.DB {
	t.Helper()
	c := Config()
	requireServer(t, c)
	return pgtestdb.New(t, c, migrator())
}

func NewURL(t testing.TB) string {
	t.Helper()
	c := Config()
	requireServer(t, c)
	return pgtestdb.Custom(t, c, migrator()).URL()
}

// NewPool — пул pgx: код на sqlc (sql_package pgx/v5) работает с ним, а не с *sql.DB.
func NewPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), NewURL(t))
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close) // закрывается раньше, чем pgtestdb удалит базу
	return pool
}

// Pools — два пула на одной чистой базе: Owner — суперпользователь (подготовка данных и
// проверки), As — соединения под ролью приложения с правами из grants.sql.
type Pools struct {
	Owner *pgxpool.Pool
	As    *pgxpool.Pool
}

// NewPoolsAs — код платформы проверяется с теми же правами, что в проде (спека §10.1, страж
// §12.7): каждое соединение As делает SET ROLE <role>. Роли api, admin, worker создаёт
// deploy/dev/initdb/20_wf.sql (в CI — шаг psql перед тестами).
func NewPoolsAs(t testing.TB, role string) Pools {
	t.Helper()
	url := NewURL(t)
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(owner.Close)
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := grants.Apply(ctx, sqldb); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	as, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool под ролью %s: %v", role, err)
	}
	t.Cleanup(as.Close)
	return Pools{Owner: owner, As: as}
}
