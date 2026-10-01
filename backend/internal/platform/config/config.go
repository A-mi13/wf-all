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
	// DocsEnabled — Swagger UI и контракт на /docs (internal/platform/apidocs): dev-стенд и
	// локальная разработка; на проде выключен.
	DocsEnabled bool `env:"DOCS_ENABLED" envDefault:"false"`
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

// Queues — конкурентность очередей воркера (спека §9.1).
type Queues struct {
	Events      int `env:"QUEUE_EVENTS" envDefault:"10"`
	Lifecycle   int `env:"QUEUE_LIFECYCLE" envDefault:"5"`
	Notify      int `env:"QUEUE_NOTIFY" envDefault:"5"`
	Mail        int `env:"QUEUE_MAIL" envDefault:"2"`
	Stats       int `env:"QUEUE_STATS" envDefault:"2"`
	Media       int `env:"QUEUE_MEDIA" envDefault:"2"`
	Maintenance int `env:"QUEUE_MAINTENANCE" envDefault:"2"`
}

// Relay — раскладка событий outbox (internal/platform/events).
type Relay struct {
	Batch int           `env:"RELAY_BATCH" envDefault:"100"`
	Poll  time.Duration `env:"RELAY_POLL" envDefault:"5s"`
}

type Worker struct {
	Log    Log
	DB     DB
	Queues Queues
	Relay  Relay
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
