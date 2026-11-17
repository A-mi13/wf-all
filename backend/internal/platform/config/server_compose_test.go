package config_test

import (
	"io/fs"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
	"go.yaml.in/yaml/v3"

	"wf/backend/internal/platform/config"
)

// Стражи файлов своего сервера deploy/server (план встроенного воркера, R8): ловятся каждым прогоном тестов бэкенда,
// без Docker; подъём compose проверяет job server в .github/workflows/image.yml.

type composeService struct {
	Command     []string          `yaml:"command"`
	Environment map[string]string `yaml:"environment"`
	DependsOn   map[string]struct {
		Condition string `yaml:"condition"`
	} `yaml:"depends_on"`
	StopGracePeriod string `yaml:"stop_grace_period"`
}

// composeServices — сервисы deploy/server/compose.yaml (якоря и «<<» раскрывает yaml).
func composeServices(t *testing.T) map[string]composeService {
	t.Helper()
	raw, err := fs.ReadFile(repoFS, "deploy/server/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("compose.yaml: %v", err)
	}
	return c.Services
}

// Спека встроенного воркера §1, §5: в прод-файле у api встроенный воркер выключен строкой "false" (перекрывает api.env),
// фон — отдельный сервис worker; migrate идёт до сервисов; окно остановки ≥ окна Render (бюджет процессов — 25 с).
func TestServerComposeNoEmbeddedWorker(t *testing.T) {
	svc := composeServices(t)
	api, ok := svc["api"]
	if !ok {
		t.Fatal("compose.yaml: нет сервиса api")
	}
	if v, ok := api.Environment["API_EMBED_WORKER"]; !ok || v != "false" {
		t.Errorf("api: API_EMBED_WORKER = %q (задан: %v), ждали \"false\" — встроенный воркер в проде запрещён", v, ok)
	}
	for name, want := range map[string][]string{"worker": {"worker"}, "migrate": {"migrate", "up"}} {
		s, ok := svc[name]
		if !ok {
			t.Errorf("compose.yaml: нет сервиса %s", name)
			continue
		}
		if strings.Join(s.Command, " ") != strings.Join(want, " ") {
			t.Errorf("%s: command %q, ждали %q", name, s.Command, want)
		}
	}
	for _, name := range []string{"api", "admin-api", "worker"} {
		s := svc[name]
		if c := s.DependsOn["migrate"].Condition; c != "service_completed_successfully" {
			t.Errorf("%s: depends_on.migrate.condition = %q, ждали service_completed_successfully", name, c)
		}
		d, err := time.ParseDuration(s.StopGracePeriod)
		if err != nil || d < config.StopWindow {
			t.Errorf("%s: stop_grace_period %q — нужно ≥ %s (бюджет процессов config.StopBudget)", name,
				s.StopGracePeriod, config.StopWindow)
		}
	}
}

// Подсеть сети wf в compose.yaml (из неё приходит Caddy) — в *_TRUSTED_PROXIES шаблонов api и admin-api (ревью infra
// m10): подсеть сменили только в compose — XFF от Caddy не принимается, у всех клиентов IP Caddy, общая корзина лимитов
// по IP и массовые 429.
func TestServerComposeSubnetTrusted(t *testing.T) {
	raw, err := fs.ReadFile(repoFS, "deploy/server/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Networks map[string]struct {
			IPAM struct {
				Config []struct {
					Subnet string `yaml:"subnet"`
				} `yaml:"config"`
			} `yaml:"ipam"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("compose.yaml: %v", err)
	}
	cfgs := c.Networks["wf"].IPAM.Config
	if len(cfgs) != 1 {
		t.Fatalf("compose.yaml: у сети wf %d подсетей ipam, ждали одну фиксированную", len(cfgs))
	}
	subnet, err := netip.ParsePrefix(cfgs[0].Subnet)
	if err != nil {
		t.Fatalf("compose.yaml: подсеть wf %q: %v", cfgs[0].Subnet, err)
	}
	for file, key := range map[string]string{"api.env.example": "API_TRUSTED_PROXIES",
		"admin-api.env.example": "ADMIN_TRUSTED_PROXIES"} {
		trusted := false
		for p := range strings.SplitSeq(envFile(t, "deploy/server/"+file)[key], ",") {
			if pp, err := netip.ParsePrefix(strings.TrimSpace(p)); err == nil && pp.Bits() <= subnet.Bits() &&
				pp.Contains(subnet.Addr()) {
				trusted = true
			}
		}
		if !trusted {
			t.Errorf("%s: %s не покрывает подсеть сети wf %s из compose.yaml — XFF от Caddy не примется", file, key, subnet)
		}
	}
}

// envFile — ключи и значения шаблона KEY=VALUE (комментарии и пустые строки пропускаются, кавычки снимаются).
func envFile(t *testing.T, name string) map[string]string {
	t.Helper()
	raw, err := fs.ReadFile(repoFS, name)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for l := range strings.Lines(string(raw)) {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			t.Errorf("%s: строка без «=»: %q", name, l)
			continue
		}
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	if len(out) == 0 {
		t.Fatalf("%s: нет ключей", name)
	}
	return out
}

// secretWords — слова ключей секретов и адресов (как в TestRenderYAMLSecretsNotInFile) плюс кольца ключей.
var secretWords = []string{"DATABASE_URL", "SECRET", "PASSWORD", "USERNAME", "SMTP_ADDR", "MAIL_FROM", "ORIGIN",
	"DEVICE_KEYS", "_SEEDS", "_KEYS"}

// wholeSecretWords — ключи, у которых всё значение — секрет: в шаблоне только один плейсхолдер целиком.
var wholeSecretWords = []string{"SECRET", "PASSWORD", "USERNAME", "DEVICE_KEYS", "_SEEDS", "_KEYS"}

// isPlaceholder — значение целиком один плейсхолдер <…>.
func isPlaceholder(v string) bool {
	return len(v) >= 2 && v[0] == '<' && v[len(v)-1] == '>' && !strings.ContainsAny(v[1:len(v)-1], "<>")
}

// urlPassword — пароль userinfo строки подключения (схема://user:pass@хост/…); ok=false — пароля нет.
func urlPassword(v string) (string, bool) {
	_, rest, ok := strings.Cut(v, "://")
	if !ok {
		return "", false
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return "", false
	}
	_, pw, ok := strings.Cut(rest[:at], ":")
	return pw, ok
}

// templateSecretProblem — что не так со значением секрета или адреса в шаблоне (пусто — в порядке): ключ не из
// secretWords — без проверки; строка подключения — пароль только плейсхолдером (ревью infra m9: настоящий пароль при
// плейсхолдере хоста); кольца, пароли, логины — значение целиком плейсхолдер; прочее (адрес, отправитель) — хоть
// один <…>.
func templateSecretProblem(k, v string) string {
	secret := false
	for _, w := range secretWords {
		secret = secret || strings.Contains(k, w)
	}
	if !secret {
		return ""
	}
	if !strings.Contains(v, "<") || !strings.Contains(v, ">") {
		return "секрет или адрес, в шаблоне только плейсхолдер <…>"
	}
	if strings.Contains(k, "DATABASE_URL") {
		if pw, ok := urlPassword(v); ok && !isPlaceholder(pw) {
			return "пароль в строке подключения шаблона — только плейсхолдер <…>"
		}
		return ""
	}
	for _, w := range wholeSecretWords {
		if strings.Contains(k, w) && !isPlaceholder(v) {
			return "секрет — значение целиком плейсхолдер <…>"
		}
	}
	return ""
}

// Проверка секретов шаблона: настоящий пароль в строке подключения при плейсхолдере хоста, кольцо или пароль с
// плейсхолдером лишь в части значения — отказ; плейсхолдеры и не-секреты — в порядке.
func TestTemplateSecretProblem(t *testing.T) {
	for _, c := range []struct {
		k, v string
		bad  bool
	}{
		{"API_DATABASE_URL", "postgres://api:<пароль>@<хост>/wf?sslmode=require", false},
		{"API_DATABASE_URL", "postgres://api@<хост>/wf", false},
		{"API_DATABASE_URL", "postgres://api:настоящий-пароль@<хост>/wf", true},
		{"MIGRATOR_DATABASE_URL", "postgres://m:p4ss<x>@<хост>/wf", true},
		{"WORKER_DATABASE_URL", "postgres://worker:p4ss@db.example/wf", true},
		{"API_JWT_SEEDS", "<base64-32-байта>", false},
		{"API_JWT_SEEDS", "<base64-32-байта>,AAAAsecret=", true},
		{"API_IDENTITY_SECRET_KEYS", "real<x>", true},
		{"ADMIN_MAIL_SMTP_PASSWORD", "<пароль-smtp>", false},
		{"ADMIN_MAIL_SMTP_PASSWORD", "hunter2", true},
		{"ADMIN_MAIL_SMTP_USERNAME", "resend<x>", true},
		{"ADMIN_MAIL_FROM", "WhereFootball <noreply@<домен>>", false},
		{"ADMIN_ORIGIN", "https://<admin-домен>", false},
		{"ADMIN_ORIGIN", "https://admin.example", true},
		{"API_IDENTITY_TOTP_ISSUER", "WF", false},
	} {
		if got := templateSecretProblem(c.k, c.v) != ""; got != c.bad {
			t.Errorf("%s=%s: отказ %v, ждали %v", c.k, c.v, got, c.bad)
		}
	}
}

// Шаблоны переменных сервисов deploy/server: каждый ключ читает процесс сервиса (опечатка молча оставила бы умолчание,
// чужой префикс — никто), секреты и адреса — плейсхолдеры <…>, встроенный воркер не включается; и полнота — каждый
// обязательный ключ (required/notEmpty) процесса есть в шаблоне, иначе сервис на сервере не стартует.
func TestServerEnvExamplesKnownKeys(t *testing.T) {
	for file, keys := range map[string]map[string]env.FieldParams{
		"api.env.example":       envKeys(t, "API_", &config.API{}),
		"admin-api.env.example": envKeys(t, "ADMIN_", &config.Admin{}),
		"worker.env.example":    workerKeys(t),
		"migrate.env.example":   envKeys(t, "MIGRATOR_", &config.Migrator{}),
	} {
		vals := envFile(t, "deploy/server/"+file)
		for k, v := range vals {
			if _, ok := keys[k]; !ok {
				t.Errorf("%s: %s — такого ключа нет в config процесса сервиса", file, k)
			}
			if msg := templateSecretProblem(k, v); msg != "" {
				t.Errorf("%s: %s — %s", file, k, msg)
			}
		}
		for k, p := range keys {
			if (p.Required || p.NotEmpty) && !p.HasDefaultValue {
				if _, ok := vals[k]; !ok {
					t.Errorf("%s: нет обязательного %s — сервис без него не стартует", file, k)
				}
			}
		}
	}
	if v, ok := envFile(t, "deploy/server/api.env.example")["API_EMBED_WORKER"]; ok && v != "false" {
		t.Errorf("api.env.example: API_EMBED_WORKER=%s — встроенный воркер в проде запрещён", v)
	}
}
