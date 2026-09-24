// backend/internal/platform/config/config.go
// Package config — типизированный конфиг бинарников из окружения.
// Каждый бинарник читает только свой префикс и получает только свои настройки.
package config

import (
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

type Log struct {
	Level  slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	Format string     `env:"LOG_FORMAT" envDefault:"json"` // json | text
}

type HTTP struct {
	Addr            string        `env:"HTTP_ADDR,required"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

type DB struct {
	URL      string `env:"DATABASE_URL,required"`
	MaxConns int32  `env:"DB_MAX_CONNS" envDefault:"10"`
}

type API struct {
	Log  Log
	HTTP HTTP
	DB   DB
}

// Admin — отдельный тип, хотя сейчас совпадает с API: конфиги разойдутся (2FA, allowlist).
type Admin struct {
	Log  Log
	HTTP HTTP
	DB   DB
}

type Worker struct {
	Log        Log
	DB         DB
	MaxWorkers int `env:"MAX_WORKERS" envDefault:"10"`
}

type Migrator struct {
	Log Log
	DB  DB
}

// Load разбирает окружение (os.Environ() в main, срез строк в тестах).
func Load[T any](prefix string, environ []string) (T, error) {
	var cfg T
	err := env.ParseWithOptions(&cfg, env.Options{
		Prefix:      prefix,
		Environment: env.ToMap(environ),
	})
	return cfg, err
}
