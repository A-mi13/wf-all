// backend/internal/platform/testkit/dbtest/dbtest.go
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

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"

	"wf/backend/migrations"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Config — подключение суперпользователем: тестам нужно CREATE DATABASE.
func Config() pgtestdb.Config {
	return pgtestdb.Config{
		DriverName: "pgx",
		Host:       env("WF_TEST_PG_HOST", "localhost"),
		Port:       env("WF_TEST_PG_PORT", "15432"),
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
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.Host, c.Port), 2*time.Second)
	if err != nil {
		t.Fatalf("Postgres недоступен на %s:%s — запусти ./task infra:up (%v)", c.Host, c.Port, err)
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
