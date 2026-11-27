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
	c, err := config.Load[config.API]("API_", append([]string{
		"API_HTTP_ADDR=:8080",
		"API_DATABASE_URL=postgres://api@localhost/wf",
		"API_JWT_SEEDS=a", "API_HUMANCHECK_KEYS=k",
	}, apiIdentity...))
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
	// встроенный воркер — только по явному флагу (dev-стенд); прод — отдельный процесс worker
	if c.EmbedWorker {
		t.Fatal("EmbedWorker по умолчанию включён")
	}
}

// R5: остановка api со встроенным воркером — дренаж + relay + мягкая River + жёсткая.
func TestAPIEmbeddedStopTime(t *testing.T) {
	if got, want := config.APIEmbeddedStopTime(7*time.Second), 7*time.Second+2*time.Second+8*time.Second+config.HardStop; got != want {
		t.Fatalf("APIEmbeddedStopTime(7s) = %s, ждали %s", got, want)
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
		"WORKER_QUEUE_MAIL=7", "WORKER_QUEUE_IMPORT=3",
		"WORKER_RELAY_POLL=2s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Queues.Mail != 7 || c.Queues.Events != 10 || c.Queues.Maintenance != 2 || c.Queues.Import != 3 {
		t.Fatalf("очереди: %+v", c.Queues)
	}
	// импорт GeoNames по умолчанию включён (R3); стенд выключает его WORKER_GEO_IMPORT_ENABLED=false
	if !c.GeoImportEnabled {
		t.Fatal("GeoImportEnabled по умолчанию — false")
	}
	d, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=postgres://w@localhost/wf",
		"WORKER_GEO_IMPORT_ENABLED=false"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Queues.Import != 1 || d.GeoImportEnabled {
		t.Fatalf("умолчание очереди import %d, флаг импорта %v", d.Queues.Import, d.GeoImportEnabled)
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
	c, err := config.Load[config.API]("API_", append(env, apiIdentity...))
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
	base := append([]string{"API_HTTP_ADDR=x", "API_DATABASE_URL=y", "API_JWT_SEEDS=a", "API_HUMANCHECK_KEYS=k"}, apiIdentity...)
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

// Без origin, ключей или почты admin-api не стартует — и называет переменную. Пустое значение
// origin и ключей — тоже отказ: required в caarlos0/env ловит только отсутствие переменной, а
// пустое кольцо ключей или origin молча дошли бы до старта.
func TestAdminRequiresStaffSettings(t *testing.T) {
	check := func(edit, name string) {
		t.Helper()
		_, err := config.Load[config.Admin]("ADMIN_", adminEnv(edit))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%q: %v", edit, err)
		}
	}
	for _, name := range []string{"ADMIN_ORIGIN", "ADMIN_STAFF_SECRET_KEYS", "ADMIN_MAIL_SMTP_ADDR", "ADMIN_MAIL_FROM"} {
		check(name, name) // переменной нет
	}
	for _, name := range []string{"ADMIN_ORIGIN", "ADMIN_STAFF_SECRET_KEYS"} {
		check(name+"=", name) // переменная пуста
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

// Ожидание схемы при старте (план C, R3): по умолчанию 10 минут — меньше 15 минут, за которые Render ждёт
// успешной проверки здоровья нового инстанса; переменная своя у каждого бинарника.
func TestDBSchemaWait(t *testing.T) {
	a, err := config.Load[config.Admin]("ADMIN_", []string{"ADMIN_HTTP_ADDR=:1", "ADMIN_DATABASE_URL=x",
		"ADMIN_ORIGIN=http://127.0.0.1:5173", "ADMIN_STAFF_SECRET_KEYS=k", "ADMIN_MAIL_SMTP_ADDR=a:1", "ADMIN_MAIL_FROM=f"})
	if err != nil {
		t.Fatal(err)
	}
	if a.DB.SchemaWait != 10*time.Minute {
		t.Fatalf("ADMIN_DB_SCHEMA_WAIT по умолчанию = %s", a.DB.SchemaWait)
	}
	w, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x", "WORKER_DB_SCHEMA_WAIT=90s"})
	if err != nil {
		t.Fatal(err)
	}
	if w.DB.SchemaWait != 90*time.Second {
		t.Fatalf("WORKER_DB_SCHEMA_WAIT = %s", w.DB.SchemaWait)
	}
}

// apiIdentity — обязательные ключи identity у api (спека identity §11.3): кольца, издатель TOTP, документы.
var apiIdentity = []string{
	"API_IDENTITY_SECRET_KEYS=s1,s0", "API_IDENTITY_DEVICE_KEYS=d1",
	"API_IDENTITY_TOTP_ISSUER=WF dev",
	"API_LEGAL_DOCUMENTS=terms=0.1.0|https://example.com/terms,privacy=0.1.0|https://example.com/privacy",
}

// Ключи identity у api (план I-2, Task 4): без любого из четырёх обязательных или с пустым значением — отказ старта
// с именем ключа; с ними — поля заполнены, кольца — в порядке (первый — текущий). Город по IP и .well-known
// необязательны: пусто — выключено (404).
func TestAPIIdentityRequired(t *testing.T) {
	base := []string{"API_HTTP_ADDR=:8080", "API_DATABASE_URL=postgres://api@localhost/wf", "API_JWT_SEEDS=a", "API_HUMANCHECK_KEYS=k"}
	for i, kv := range apiIdentity {
		name, _, _ := strings.Cut(kv, "=")
		rest := append(append([]string{}, apiIdentity[:i]...), apiIdentity[i+1:]...)
		for _, env := range [][]string{rest, append(rest, name+"=")} {
			_, err := config.Load[config.API]("API_", append(append([]string{}, base...), env...))
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("без %s (%d переменных): err = %v", name, len(env), err)
			}
		}
	}
	c, err := config.Load[config.API]("API_", append(append([]string{}, base...), append(apiIdentity,
		"API_IPGEO_DB=/var/lib/wf/dbip-city-lite.mmdb",
		"API_WELLKNOWN_APPLE_APP_IDS=TEAM1.app.wf,TEAM1.app.wf.dev",
		"API_WELLKNOWN_ANDROID=app.wf=AA:BB;app.wf.dev=CC:DD")...))
	if err != nil {
		t.Fatal(err)
	}
	id := c.Identity
	if !reflect.DeepEqual(id.SecretKeys, []string{"s1", "s0"}) || !reflect.DeepEqual(id.DeviceKeys, []string{"d1"}) ||
		id.TOTPIssuer != "WF dev" {
		t.Fatalf("identity: %+v", id)
	}
	if c.LegalDocuments != "terms=0.1.0|https://example.com/terms,privacy=0.1.0|https://example.com/privacy" ||
		c.IPGeoDB != "/var/lib/wf/dbip-city-lite.mmdb" {
		t.Fatalf("документы и ipgeo: %q %q", c.LegalDocuments, c.IPGeoDB)
	}
	if !reflect.DeepEqual(c.WellKnown.AppleAppIDs, []string{"TEAM1.app.wf", "TEAM1.app.wf.dev"}) ||
		c.WellKnown.Android != "app.wf=AA:BB;app.wf.dev=CC:DD" {
		t.Fatalf("well-known: %+v", c.WellKnown)
	}
	c, err = config.Load[config.API]("API_", append(append([]string{}, base...), apiIdentity...))
	if err != nil || c.IPGeoDB != "" || len(c.WellKnown.AppleAppIDs) != 0 || c.WellKnown.Android != "" {
		t.Fatalf("необязательные по умолчанию пусты: %v %+v", err, c)
	}
}

// Кольцо identity воркера (WORKER_IDENTITY_SECRET_KEYS) — отдельный конфиг, как почта: его читает только режим
// воркера (письма с кодами), разовым командам (events replay, geo import) оно не нужно. Нет или пусто — ошибка.
func TestWorkerIdentityRequired(t *testing.T) {
	for _, env := range [][]string{nil, {"WORKER_IDENTITY_SECRET_KEYS="}} {
		if _, err := config.Load[config.WorkerIdentity]("WORKER_", env); err == nil ||
			!strings.Contains(err.Error(), "WORKER_IDENTITY_SECRET_KEYS") {
			t.Errorf("%v: err = %v", env, err)
		}
	}
	c, err := config.Load[config.WorkerIdentity]("WORKER_", []string{"WORKER_IDENTITY_SECRET_KEYS=s1,s0"})
	if err != nil || !reflect.DeepEqual(c.SecretKeys, []string{"s1", "s0"}) {
		t.Fatalf("%v %+v", err, c.SecretKeys)
	}
	if _, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=x"}); err != nil {
		t.Fatalf("конфиг воркера без кольца (разовые команды): %v", err)
	}
}

// Кольцо identity — только у api и воркера: admin-api сброс 2FA не шифрует и не расшифровывает (спека §5.5).
func TestAdminHasNoIdentityKeys(t *testing.T) {
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for f := range rt.Fields() {
			if strings.Contains(f.Tag.Get("env"), "IDENTITY") || strings.Contains(f.Tag.Get("envPrefix"), "IDENTITY") {
				t.Errorf("config.Admin: поле %s", f.Name)
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type)
			}
		}
	}
	walk(reflect.TypeFor[config.Admin]())
}

// Побудка api из admin-api (спека встроенного воркера §8.1): по умолчанию выключена (пусто), адрес — как задан.
func TestLoadAdminWakeAPIURL(t *testing.T) {
	c, err := config.Load[config.Admin]("ADMIN_", adminEnv())
	if err != nil {
		t.Fatal(err)
	}
	if c.WakeAPIURL != "" {
		t.Fatalf("WakeAPIURL по умолчанию %q", c.WakeAPIURL)
	}
	c, err = config.Load[config.Admin]("ADMIN_", adminEnv("ADMIN_WAKE_API_URL=https://wf-api.onrender.com"))
	if err != nil {
		t.Fatal(err)
	}
	if c.WakeAPIURL != "https://wf-api.onrender.com" {
		t.Fatalf("WakeAPIURL = %q", c.WakeAPIURL)
	}
}

// blobDevKey — годный ключ подписи FS (32 байта в base64) для тестов.
var blobDevKey = strings.Repeat("B", 43) + "="

// blobEnv — полный набор ключей режима mode с префиксом prefix и правками: "KEY=value" (без префикса) заменяет или
// добавляет, "KEY" без «=» убирает.
func blobEnv(prefix, mode string, edits ...string) []string {
	env := map[string]string{"BLOB_BACKEND": mode}
	switch mode {
	case "fs":
		env["BLOB_FS_ROOT"] = "../.tools/blob"
		env["BLOB_FS_BASE_URL"] = "http://127.0.0.1:8080"
		env["BLOB_DEV_SIGN_KEY"] = blobDevKey
	case "s3":
		env["BLOB_S3_ENDPOINT"] = "http://garage:3900"
		env["BLOB_S3_PUBLIC_ENDPOINT"] = "https://s3.example"
		env["BLOB_S3_REGION"] = "garage"
		env["BLOB_S3_BUCKET_IN"] = "wf-in"
		env["BLOB_S3_BUCKET_PUB"] = "wf-pub"
		env["BLOB_S3_BUCKET_PRIV"] = "wf-priv"
		env["BLOB_S3_ACCESS_KEY"] = "GKtest"
		env["BLOB_S3_SECRET_KEY"] = "s3-secret-value"
		env["MEDIA_PUBLIC_BASE_URL"] = "https://media.example/"
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
		out = append(out, prefix+k+"="+v)
	}
	return out
}

// Спека media §6.4, R17: режимы хранилища у трёх процессов. none — прочие ключи не нужны и не читаются; fs — корень,
// внешний адрес api и ключ подписи; s3 — все S3_* и адрес публичного медиа; иное или пустое значение — отказ. В
// ошибке — полное имя ключа с префиксом процесса, без значений секретов.
func TestBlobConfigModes(t *testing.T) {
	s3Keys := []string{"BLOB_S3_ENDPOINT", "BLOB_S3_PUBLIC_ENDPOINT", "BLOB_S3_REGION", "BLOB_S3_BUCKET_IN",
		"BLOB_S3_BUCKET_PUB", "BLOB_S3_BUCKET_PRIV", "BLOB_S3_ACCESS_KEY", "BLOB_S3_SECRET_KEY", "MEDIA_PUBLIC_BASE_URL"}
	type tc struct {
		name    string
		prefix  string
		env     []string
		wantErr string // "" — без ошибки
	}
	cases := []tc{
		{"none один", "API_", blobEnv("API_", "none"), ""},
		{"none с мусором прочих ключей", "WORKER_", blobEnv("WORKER_", "none", "BLOB_FS_ROOT=x",
			"BLOB_DEV_SIGN_KEY=короткий", "BLOB_S3_ENDPOINT=не-адрес", "MEDIA_PUBLIC_BASE_URL=x"), ""},
		{"fs полный", "API_", blobEnv("API_", "fs"), ""},
		{"fs и совпадающий адрес медиа", "API_", blobEnv("API_", "fs",
			"MEDIA_PUBLIC_BASE_URL=http://127.0.0.1:8080/_media/pub/"), ""},
		{"fs без ключа подписи", "API_", blobEnv("API_", "fs", "BLOB_DEV_SIGN_KEY"), "API_BLOB_DEV_SIGN_KEY"},
		{"fs с пустым ключом подписи", "ADMIN_", blobEnv("ADMIN_", "fs", "BLOB_DEV_SIGN_KEY="), "ADMIN_BLOB_DEV_SIGN_KEY"},
		{"fs с ключом короче 32 байт", "API_", blobEnv("API_", "fs", "BLOB_DEV_SIGN_KEY=c2hvcnQ="), "API_BLOB_DEV_SIGN_KEY"},
		{"fs с ключом не в base64", "API_", blobEnv("API_", "fs", "BLOB_DEV_SIGN_KEY="+strings.Repeat("!", 44)),
			"API_BLOB_DEV_SIGN_KEY"},
		{"fs без корня", "WORKER_", blobEnv("WORKER_", "fs", "BLOB_FS_ROOT"), "WORKER_BLOB_FS_ROOT"},
		{"fs без адреса api", "API_", blobEnv("API_", "fs", "BLOB_FS_BASE_URL"), "API_BLOB_FS_BASE_URL"},
		{"fs с адресом без схемы", "API_", blobEnv("API_", "fs", "BLOB_FS_BASE_URL=127.0.0.1:8080"), "API_BLOB_FS_BASE_URL"},
		{"fs с адресом с запросом", "API_", blobEnv("API_", "fs", "BLOB_FS_BASE_URL=http://127.0.0.1:8080/?a=1"),
			"API_BLOB_FS_BASE_URL"},
		{"fs и другой адрес медиа", "API_", blobEnv("API_", "fs", "MEDIA_PUBLIC_BASE_URL=https://cdn.example/"),
			"API_MEDIA_PUBLIC_BASE_URL"},
		{"s3 полный", "ADMIN_", blobEnv("ADMIN_", "s3"), ""},
		{"BACKEND нет", "API_", blobEnv("API_", "none", "BLOB_BACKEND"), "API_BLOB_BACKEND"},
		{"BACKEND пусто", "API_", blobEnv("API_", "none", "BLOB_BACKEND="), "API_BLOB_BACKEND"},
		{"BACKEND minio", "API_", blobEnv("API_", "minio"), "API_BLOB_BACKEND"},
		{"BACKEND в верхнем регистре", "API_", blobEnv("API_", "FS"), "API_BLOB_BACKEND"},
	}
	for _, k := range s3Keys {
		cases = append(cases, tc{"s3 без " + k, "API_", blobEnv("API_", "s3", k), "API_" + k},
			tc{"s3 с пустым " + k, "WORKER_", blobEnv("WORKER_", "s3", k+"="), "WORKER_" + k})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := config.LoadBlob(c.prefix, c.env)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("ждали без ошибки: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, ждали упоминание %s", err, c.wantErr)
			}
			for _, secret := range []string{blobDevKey, "s3-secret-value", "c2hvcnQ="} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("в ошибке значение секрета: %v", err)
				}
			}
		})
	}
	// поля и адрес публичного медиа: у fs — выводится из адреса api (R17), у s3 — из ключа, у none — пусто
	fs, err := config.LoadBlob("API_", blobEnv("API_", "fs", "BLOB_FS_BASE_URL=http://127.0.0.1:8080/"))
	if err != nil {
		t.Fatal(err)
	}
	if fs.Backend != "fs" || fs.FSRoot != "../.tools/blob" || fs.DevSignKey != blobDevKey {
		t.Fatalf("fs: %+v", fs)
	}
	if got := fs.PublicBaseURL(); got != "http://127.0.0.1:8080/_media/pub/" {
		t.Fatalf("fs: PublicBaseURL = %q", got)
	}
	s3, err := config.LoadBlob("ADMIN_", blobEnv("ADMIN_", "s3"))
	if err != nil {
		t.Fatal(err)
	}
	if s3.S3Endpoint != "http://garage:3900" || s3.S3PublicEndpoint != "https://s3.example" || s3.S3Region != "garage" ||
		s3.S3BucketIn != "wf-in" || s3.S3BucketPub != "wf-pub" || s3.S3BucketPriv != "wf-priv" ||
		s3.S3AccessKey != "GKtest" || s3.S3SecretKey != "s3-secret-value" || s3.PublicBaseURL() != "https://media.example/" {
		t.Fatalf("s3: %+v", s3)
	}
	none, err := config.LoadBlob("API_", blobEnv("API_", "none", "MEDIA_PUBLIC_BASE_URL=https://media.example/"))
	if err != nil || none.PublicBaseURL() != "" {
		t.Fatalf("none: %v %q", err, none.PublicBaseURL())
	}
}

// R15: в прод-образе (WF_BUILD_KIND=prod из backend/Dockerfile) fs не принимается — ошибка старта с именем ключа;
// none и s3 — в порядке. Признак читается из окружения процесса (LoadBlob) и передаётся в Validate.
func TestBlobFSRejectedInProdBuild(t *testing.T) {
	for _, prefix := range []string{"API_", "ADMIN_", "WORKER_"} {
		_, err := config.LoadBlob(prefix, append(blobEnv(prefix, "fs"), "WF_BUILD_KIND=prod"))
		if err == nil || !strings.Contains(err.Error(), prefix+"BLOB_BACKEND") || !strings.Contains(err.Error(), "WF_BUILD_KIND") {
			t.Errorf("%s: prod + fs: err = %v, ждали отказ с %sBLOB_BACKEND и WF_BUILD_KIND", prefix, err, prefix)
		}
		for _, mode := range []string{"none", "s3"} {
			if _, err := config.LoadBlob(prefix, append(blobEnv(prefix, mode), "WF_BUILD_KIND=prod")); err != nil {
				t.Errorf("%s: prod + %s: %v", prefix, mode, err)
			}
		}
		// без признака (dev, e2e) fs принимается
		if _, err := config.LoadBlob(prefix, blobEnv(prefix, "fs")); err != nil {
			t.Errorf("%s: fs без WF_BUILD_KIND: %v", prefix, err)
		}
	}
	c, err := config.Load[config.Blob]("API_", blobEnv("API_", "fs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate("API_", "prod"); err == nil || !strings.Contains(err.Error(), "API_BLOB_BACKEND") {
		t.Fatalf("Validate(prod) fs: %v", err)
	}
	if err := c.Validate("API_", ""); err != nil {
		t.Fatalf("Validate(\"\") fs: %v", err)
	}
}
