package config_test

import (
	"log/slog"
	"net/netip"
	"reflect"
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

// adminEnv — обязательные переменные admin-api с правками: "KEY=value" заменяет или добавляет,
// "KEY" без = убирает.
func adminEnv(edits ...string) []string {
	env := map[string]string{
		"ADMIN_HTTP_ADDR":         ":8081",
		"ADMIN_DATABASE_URL":      "postgres://admin@localhost/wf",
		"ADMIN_ORIGIN":            "http://127.0.0.1:5173",
		"ADMIN_STAFF_SECRET_KEYS": "k1",
		"ADMIN_MAIL_SMTP_ADDR":    "127.0.0.1:11025",
		"ADMIN_MAIL_FROM":         "WF <noreply@wf.local>",
	}
	for _, e := range edits {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		} else {
			delete(env, k)
		}
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func TestLoadDocsEnabledPerBinary(t *testing.T) {
	c, err := config.Load[config.Admin]("ADMIN_", adminEnv("ADMIN_DOCS_ENABLED=true", "API_DOCS_ENABLED=false"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.HTTP.DocsEnabled {
		t.Fatal("ADMIN_DOCS_ENABLED=true не включил Swagger админки")
	}
}

// admin-api BFF не принимает (фронт админки ходит в API напрямую): переменных ADMIN_BFF_* нет —
// иначе они принимались бы и ни на что не влияли. Прокси хостинга — ADMIN_TRUSTED_PROXIES.
func TestAdminHasNoBFF(t *testing.T) {
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for f := range rt.Fields() {
			if strings.HasPrefix(f.Tag.Get("env"), "BFF_") {
				t.Errorf("config.Admin: поле %s (ADMIN_%s)", f.Name, f.Tag.Get("env"))
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type)
			}
		}
	}
	walk(reflect.TypeFor[config.Admin]())
	c, err := config.Load[config.Admin]("ADMIN_", adminEnv("ADMIN_TRUSTED_PROXIES=10.0.0.0/8"))
	if err != nil || len(c.TrustedProxies) != 1 || c.TrustedProxies[0] != netip.MustParsePrefix("10.0.0.0/8") {
		t.Fatalf("прокси: %v %+v", err, c.TrustedProxies)
	}
}

// Заголовок с адресом посетителя от CDN — ADMIN_CLIENT_IP_HEADER; по умолчанию пусто.
func TestAdminClientIPHeader(t *testing.T) {
	base := adminEnv()
	c, err := config.Load[config.Admin]("ADMIN_", adminEnv("ADMIN_CLIENT_IP_HEADER=CF-Connecting-IP"))
	if err != nil || c.ClientIPHeader != "CF-Connecting-IP" {
		t.Fatalf("%v %q", err, c.ClientIPHeader)
	}
	if c, err = config.Load[config.Admin]("ADMIN_", base); err != nil || c.ClientIPHeader != "" {
		t.Fatalf("по умолчанию: %v %q", err, c.ClientIPHeader)
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

// Источник GeoNames импорта geo (спека geo §5.4): по умолчанию download.geonames.org, 50 и 200 МБ.
func TestLoadWorkerGeoNames(t *testing.T) {
	c, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x"})
	if err != nil {
		t.Fatal(err)
	}
	if g := c.GeoNames; g.BaseURL != "https://download.geonames.org" || g.MaxCompressed != 50<<20 || g.MaxUncompressed != 200<<20 {
		t.Fatalf("по умолчанию: %+v", g)
	}
	c, err = config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x",
		"WORKER_GEONAMES_BASE_URL=https://mirror.example", "WORKER_GEONAMES_MAX_COMPRESSED=1024",
		"WORKER_GEONAMES_MAX_UNCOMPRESSED=2048"})
	if err != nil {
		t.Fatal(err)
	}
	if g := c.GeoNames; g.BaseURL != "https://mirror.example" || g.MaxCompressed != 1024 || g.MaxUncompressed != 2048 {
		t.Fatalf("из окружения: %+v", g)
	}
}

func TestAPIConfig(t *testing.T) {
	env := []string{
		"API_HTTP_ADDR=127.0.0.1:0", "API_DATABASE_URL=postgres://x",
		"API_JWT_SEEDS=a,b", "API_HUMANCHECK_KEYS=k",
		"API_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12",
		"API_BFF_NETS=192.0.2.0/24", "API_BFF_SECRETS=s1,s2",
		"API_CLIENT_IP_HEADER=CF-Connecting-IP",
		"API_RATE_LIMITS=auth.ip=10/1m",
	}
	c, err := config.Load[config.API]("API_", env)
	if err != nil {
		t.Fatal(err)
	}
	if c.Peer.ClientIPHeader != "CF-Connecting-IP" {
		t.Fatalf("заголовок CDN: %q", c.Peer.ClientIPHeader)
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

// Почта — отдельный конфиг: её читает только воркер, разовые команды (events replay) — нет.
func TestWorkerMailConfig(t *testing.T) {
	c, err := config.Load[config.Mail]("WORKER_", []string{
		"WORKER_MAIL_SMTP_ADDR=127.0.0.1:11025", "WORKER_MAIL_FROM=WF <noreply@wf.local>"})
	if err != nil {
		t.Fatal(err)
	}
	if c.TLS != "mandatory" || c.SMTPAddr != "127.0.0.1:11025" {
		t.Fatalf("%+v", c)
	}
	if _, err := config.Load[config.Mail]("WORKER_", nil); err == nil ||
		!strings.Contains(err.Error(), "WORKER_MAIL_SMTP_ADDR") {
		t.Fatalf("без почты: %v", err)
	}
	if _, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x"}); err != nil {
		t.Fatalf("конфиг воркера без почты: %v", err)
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

// Вход сотрудников (спека среза §5): origin, кольцо ключей, аллоулист, Secure-cookie, статика и
// почта admin-api.
func TestAdminStaffConfig(t *testing.T) {
	c, err := config.Load[config.Admin]("ADMIN_", adminEnv(
		"ADMIN_STAFF_SECRET_KEYS=new,old",
		"ADMIN_IP_ALLOWLIST=203.0.113.0/24,2001:db8::/32",
		"ADMIN_STATIC_DIR=../apps/admin/dist",
		"ADMIN_MAIL_SMTP_TLS=none",
	))
	if err != nil {
		t.Fatal(err)
	}
	if c.Origin != "http://127.0.0.1:5173" || len(c.StaffSecretKeys) != 2 || c.StaffSecretKeys[0] != "new" ||
		c.StaticDir != "../apps/admin/dist" {
		t.Fatalf("%+v", c)
	}
	if len(c.IPAllowlist) != 2 || c.IPAllowlist[1] != netip.MustParsePrefix("2001:db8::/32") {
		t.Fatalf("аллоулист: %+v", c.IPAllowlist)
	}
	if !c.CookieSecure {
		t.Fatal("ADMIN_COOKIE_SECURE по умолчанию не true")
	}
	if c.Mail.SMTPAddr != "127.0.0.1:11025" || c.Mail.TLS != "none" || c.Mail.From == "" {
		t.Fatalf("почта: %+v", c.Mail)
	}

	c, err = config.Load[config.Admin]("ADMIN_", adminEnv("ADMIN_COOKIE_SECURE=false", "ADMIN_IP_ALLOWLIST="))
	if err != nil || c.CookieSecure || len(c.IPAllowlist) != 0 || c.StaticDir != "" || c.Mail.TLS != "mandatory" {
		t.Fatalf("dev без Secure, пустой аллоулист: %v %+v", err, c)
	}
}

// Без origin, ключей или почты admin-api не стартует — и называет переменную.
func TestAdminRequiresStaffSettings(t *testing.T) {
	for _, name := range []string{"ADMIN_ORIGIN", "ADMIN_STAFF_SECRET_KEYS", "ADMIN_MAIL_SMTP_ADDR", "ADMIN_MAIL_FROM"} {
		_, err := config.Load[config.Admin]("ADMIN_", adminEnv(name))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("без %s: %v", name, err)
		}
	}
}

// Ключей сотрудников у воркера нет (решение D3): письма и перешифровка — в admin-api.
func TestWorkerHasNoStaffKeys(t *testing.T) {
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for f := range rt.Fields() {
			if strings.Contains(f.Tag.Get("env"), "STAFF") {
				t.Errorf("config.Worker: поле %s (WORKER_%s)", f.Name, f.Tag.Get("env"))
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type)
			}
		}
	}
	walk(reflect.TypeFor[config.Worker]())
}
