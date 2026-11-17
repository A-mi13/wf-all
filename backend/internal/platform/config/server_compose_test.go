package config_test

import (
	"io/fs"
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
			for _, w := range secretWords {
				if strings.Contains(k, w) && (!strings.Contains(v, "<") || !strings.Contains(v, ">")) {
					t.Errorf("%s: %s — секрет или адрес, в шаблоне только плейсхолдер <…>", file, k)
				}
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
