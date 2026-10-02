// Package config — типизированный конфиг бинарников из окружения.
// Каждый бинарник читает только свой префикс и получает только свои настройки.
package config

import (
	"log/slog"
	"net/netip"
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
	// RequestTimeout — крайний срок запроса (спека §6.1): запросы к базе прерываются по нему.
	RequestTimeout time.Duration `env:"REQUEST_TIMEOUT" envDefault:"15s"`
	// DocsEnabled — Swagger UI и контракт на /docs (internal/platform/apidocs): dev-стенд и
	// локальная разработка; на проде выключен.
	DocsEnabled bool `env:"DOCS_ENABLED" envDefault:"false"`
}

type DB struct {
	URL      string `env:"DATABASE_URL,required"`
	MaxConns int32  `env:"DB_MAX_CONNS" envDefault:"10"`
}

// Auth — access-токены публичного API (спека §6.2).
type Auth struct {
	// JWTSeeds — сиды Ed25519 (base64, 32 байта) через запятую: первый подписывает, все
	// проверяют. Ротация: новый ключ первым, старый — следом на время жизни access (10 мин).
	JWTSeeds []string `env:"JWT_SEEDS,required" envSeparator:","`
}

// Peer — кому верить заголовкам IP (спека §8.4).
type Peer struct {
	TrustedProxies []netip.Prefix `env:"TRUSTED_PROXIES" envSeparator:","` // балансировщик хостинга
	BFFNets        []netip.Prefix `env:"BFF_NETS" envSeparator:","`        // адреса BFF (Next.js)
	BFFSecrets     []string       `env:"BFF_SECRETS" envSeparator:","`     // секрет BFF ↔ API, ≥ 32 символов
}

// Humancheck — PoW антибота (спека §6.7).
type Humancheck struct {
	Keys      []string      `env:"HUMANCHECK_KEYS,required" envSeparator:","` // HMAC, base64, ≥ 32 байт
	TTL       time.Duration `env:"HUMANCHECK_TTL" envDefault:"5m"`
	MaxNumber int64         `env:"HUMANCHECK_MAX_NUMBER" envDefault:"100000"`
}

type API struct {
	Log        Log
	HTTP       HTTP
	DB         DB
	Auth       Auth
	Peer       Peer
	RateLimits string `env:"RATE_LIMITS"` // переопределения ratelimit.DefaultRules: auth.ip=30/1m:10,…
	Humancheck Humancheck
}

// Admin — вход сотрудников (сессия, TOTP, аллоулист) добавит спека identity.
type Admin struct {
	Log        Log
	HTTP       HTTP
	DB         DB
	Peer       Peer
	RateLimits string `env:"RATE_LIMITS"`
}

// Mail — SMTP транзакционных писем (спека §6.9); в dev — Mailpit.
type Mail struct {
	SMTPAddr string `env:"MAIL_SMTP_ADDR,required"`
	From     string `env:"MAIL_FROM,required"`
	Username string `env:"MAIL_SMTP_USERNAME"`
	Password string `env:"MAIL_SMTP_PASSWORD"`
	TLS      string `env:"MAIL_SMTP_TLS" envDefault:"mandatory"` // mandatory | opportunistic | none
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
	Mail   Mail
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
