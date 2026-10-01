package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/config"
)

func TestLoadAPIWithDefaults(t *testing.T) {
	c, err := config.Load[config.API]("API_", []string{
		"API_HTTP_ADDR=:8080",
		"API_DATABASE_URL=postgres://api@localhost/wf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.Addr != ":8080" || c.HTTP.ShutdownTimeout != 10*time.Second {
		t.Fatalf("http: %+v", c.HTTP)
	}
	if c.DB.MaxConns != 10 || c.Log.Level != slog.LevelInfo || c.Log.Format != "json" {
		t.Fatalf("defaults: %+v", c)
	}
	// Swagger выключен, пока его не включили явно: на проде контракт наружу не светится
	if c.HTTP.DocsEnabled {
		t.Fatal("DocsEnabled по умолчанию включён")
	}
}

func TestLoadDocsEnabledPerBinary(t *testing.T) {
	c, err := config.Load[config.Admin]("ADMIN_", []string{
		"ADMIN_HTTP_ADDR=:8081",
		"ADMIN_DATABASE_URL=postgres://admin@localhost/wf",
		"ADMIN_DOCS_ENABLED=true",
		"API_DOCS_ENABLED=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.HTTP.DocsEnabled {
		t.Fatal("ADMIN_DOCS_ENABLED=true не включил Swagger админки")
	}
}

// Бинарник без обязательной переменной не должен стартовать молча.
func TestLoadFailsOnMissingRequiredAndNamesIt(t *testing.T) {
	_, err := config.Load[config.API]("API_", []string{"API_HTTP_ADDR=:8080"})
	if err == nil || !strings.Contains(err.Error(), "API_DATABASE_URL") {
		t.Fatalf("err = %v, ждали упоминание API_DATABASE_URL", err)
	}
}

func TestLoadParsesLogLevel(t *testing.T) {
	c, err := config.Load[config.Migrator]("MIGRATOR_", []string{
		"MIGRATOR_DATABASE_URL=postgres://m@localhost/wf",
		"MIGRATOR_LOG_LEVEL=debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Log.Level != slog.LevelDebug {
		t.Fatalf("level = %v", c.Log.Level)
	}
}
