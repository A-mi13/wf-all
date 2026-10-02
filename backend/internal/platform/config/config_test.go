package config_test

import (
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/config"
)

func TestLoadAPIWithDefaults(t *testing.T) {
	c, err := config.Load[config.API]("API_", []string{
		"API_HTTP_ADDR=:8080",
		"API_DATABASE_URL=postgres://api@localhost/wf",
		"API_JWT_SEEDS=a", "API_HUMANCHECK_KEYS=k",
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

func TestLoadWorkerQueuesAndRelay(t *testing.T) {
	c, err := config.Load[config.Worker]("WORKER_", []string{
		"WORKER_DATABASE_URL=postgres://w@localhost/wf",
		"WORKER_MAIL_SMTP_ADDR=127.0.0.1:11025", "WORKER_MAIL_FROM=WF <noreply@wf.local>",
		"WORKER_QUEUE_MAIL=7",
		"WORKER_RELAY_POLL=2s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Queues.Mail != 7 || c.Queues.Events != 10 || c.Queues.Maintenance != 2 {
		t.Fatalf("очереди: %+v", c.Queues)
	}
	if c.Relay.Poll != 2*time.Second || c.Relay.Batch != 100 {
		t.Fatalf("relay: %+v", c.Relay)
	}
}

func TestAPIConfig(t *testing.T) {
	env := []string{
		"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://x",
		"API_JWT_SEEDS=a,b", "API_HUMANCHECK_KEYS=k",
		"API_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12",
		"API_BFF_NETS=192.0.2.0/24", "API_BFF_SECRETS=s1,s2",
		"API_RATE_LIMITS=auth.ip=10/1m",
	}
	c, err := config.Load[config.API]("API_", env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Auth.JWTSeeds) != 2 || c.Humancheck.TTL != 5*time.Minute || c.Humancheck.MaxNumber != 100000 ||
		c.HTTP.RequestTimeout != 15*time.Second || c.RateLimits != "auth.ip=10/1m" {
		t.Fatalf("%+v", c)
	}
	if len(c.Peer.TrustedProxies) != 2 || c.Peer.TrustedProxies[1] != netip.MustParsePrefix("172.16.0.0/12") ||
		len(c.Peer.BFFSecrets) != 2 {
		t.Fatalf("peer: %+v", c.Peer)
	}
}

func TestAPIConfigRequiresKeys(t *testing.T) {
	_, err := config.Load[config.API]("API_", []string{"API_HTTP_ADDR=x", "API_DATABASE_URL=y"})
	if err == nil || !strings.Contains(err.Error(), "API_JWT_SEEDS") || !strings.Contains(err.Error(), "API_HUMANCHECK_KEYS") {
		t.Fatalf("err = %v", err)
	}
}

func TestWorkerMailConfig(t *testing.T) {
	c, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x",
		"WORKER_MAIL_SMTP_ADDR=127.0.0.1:11025", "WORKER_MAIL_FROM=WF <noreply@wf.local>"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Mail.TLS != "mandatory" || c.Mail.SMTPAddr != "127.0.0.1:11025" {
		t.Fatalf("%+v", c.Mail)
	}
	if _, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x"}); err == nil ||
		!strings.Contains(err.Error(), "WORKER_MAIL_SMTP_ADDR") {
		t.Fatalf("без почты: %v", err)
	}
}

// Пустой список сетей (локально прокси нет) — допустим. Висячая запятая даёт пустой элемент:
// netip.Prefix разбирает "" без ошибки в нулевой (невалидный) префикс — его отвергает
// peer.Config.Validate на старте (cmd/api, cmd/admin-api), а не молча доверяет пустоте.
func TestPeerNetsEmptyAndTrailingComma(t *testing.T) {
	base := []string{"API_HTTP_ADDR=x", "API_DATABASE_URL=y", "API_JWT_SEEDS=a", "API_HUMANCHECK_KEYS=k"}
	c, err := config.Load[config.API]("API_", append(base, "API_TRUSTED_PROXIES=", "API_BFF_NETS="))
	if err != nil || len(c.Peer.TrustedProxies) != 0 || len(c.Peer.BFFNets) != 0 {
		t.Fatalf("пустые списки: %v %+v", err, c.Peer)
	}
	c, err = config.Load[config.API]("API_", append(base, "API_TRUSTED_PROXIES=10.0.0.0/8,"))
	if err != nil || len(c.Peer.TrustedProxies) != 2 || c.Peer.TrustedProxies[1].IsValid() {
		t.Fatalf("висячая запятая: %v %+v", err, c.Peer)
	}
}
