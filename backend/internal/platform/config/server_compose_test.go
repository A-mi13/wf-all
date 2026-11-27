package config_test

import (
	"io/fs"
	"net/netip"
	"slices"
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
	Image       string            `yaml:"image"`
	Command     []string          `yaml:"command"`
	Environment map[string]string `yaml:"environment"`
	DependsOn   map[string]struct {
		Condition string `yaml:"condition"`
	} `yaml:"depends_on"`
	StopGracePeriod string   `yaml:"stop_grace_period"`
	Volumes         []string `yaml:"volumes"`
	EnvFile         envFiles `yaml:"env_file"`
	Ports           []any    `yaml:"ports"`
	Healthcheck     *struct {
		Test []string `yaml:"test"`
	} `yaml:"healthcheck"`
}

// envFiles — env_file сервиса: строка или список строк.
type envFiles []string

func (e *envFiles) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*e = envFiles{n.Value}
		return nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return err
	}
	*e = l
	return nil
}

// composeServices — сервисы deploy/server/compose.yaml (якоря и «<<» раскрывает yaml).
func composeServices(t *testing.T) map[string]composeService {
	return composeFileServices(t, "deploy/server/compose.yaml")
}

// composeFileServices — сервисы файла compose name.
func composeFileServices(t *testing.T, name string) map[string]composeService {
	t.Helper()
	raw, err := fs.ReadFile(repoFS, name)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%s: %v", name, err)
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

// secretWords — слова ключей секретов и адресов (как в TestRenderYAMLSecretsNotInFile) плюс кольца ключей и ключи
// хранилища (*_BLOB_S3_ACCESS_KEY, *_BLOB_DEV_SIGN_KEY: «_KEYS» их не ловит).
var secretWords = []string{"DATABASE_URL", "SECRET", "PASSWORD", "USERNAME", "SMTP_ADDR", "MAIL_FROM", "ORIGIN",
	"DEVICE_KEYS", "_SEEDS", "_KEYS", "ACCESS_KEY", "SIGN_KEY", "TOKEN"}

// wholeSecretWords — ключи, у которых всё значение — секрет: в шаблоне только один плейсхолдер целиком.
var wholeSecretWords = []string{"SECRET", "PASSWORD", "USERNAME", "DEVICE_KEYS", "_SEEDS", "_KEYS", "ACCESS_KEY",
	"SIGN_KEY", "TOKEN"}

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
		{"API_BLOB_S3_ACCESS_KEY", "<ключ-доступа-api>", false},
		{"API_BLOB_S3_ACCESS_KEY", "GK31c2f218a2e44f485b94239e", true}, // gitleaks:allow — нарочно похоже на ключ: страж должен его поймать
		{"WORKER_BLOB_S3_ACCESS_KEY", "GK<id>", true},
		{"API_BLOB_DEV_SIGN_KEY", "<base64-32-байта>", false},
		{"API_BLOB_DEV_SIGN_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", true},
		{"API_BLOB_S3_BUCKET_IN", "wf-in", false},
		{"GARAGE_ADMIN_TOKEN", "<base64-32-байта>", false},
		{"GARAGE_ADMIN_TOKEN", "c2VjcmV0LXRva2Vu", true},
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
		"worker.env.example":    envKeys(t, "WORKER_", &config.Worker{}, &config.Mail{}, &config.WorkerIdentity{}),
		"migrate.env.example":   envKeys(t, "MIGRATOR_", &config.Migrator{}),
		// ключи хранилища процесса — отдельным файлом: его же получает storage-init (ревью Task 12, m1)
		"api.blob.env.example":       envKeys(t, "API_", &config.Blob{}),
		"admin-api.blob.env.example": envKeys(t, "ADMIN_", &config.Blob{}),
		"worker.blob.env.example":    envKeys(t, "WORKER_", &config.Blob{}),
		// не процесс бэкенда: секреты Garage для storage и storage-init (compose.yaml), оба обязательны
		"storage.env.example": {"GARAGE_RPC_SECRET": {Required: true}, "GARAGE_ADMIN_TOKEN": {Required: true}},
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

// Спека media §6.4, §6.5: свой сервер — хранилище S3 у всех трёх процессов (fs запрещён в прод-образе, none — только
// стенд); шаблоны ключей хранилища с плейсхолдерами проходят config.LoadBlob в прод-образе (набор ключей и формат адресов).
func TestServerEnvExamplesBlobS3(t *testing.T) {
	for file, prefix := range map[string]string{"api.blob.env.example": "API_", "admin-api.blob.env.example": "ADMIN_",
		"worker.blob.env.example": "WORKER_"} {
		vals := envFile(t, "deploy/server/"+file)
		if vals[prefix+"BLOB_BACKEND"] != "s3" {
			t.Errorf("%s: %sBLOB_BACKEND = %q, ждали s3", file, prefix, vals[prefix+"BLOB_BACKEND"])
		}
		environ := []string{config.BuildKindVar + "=" + config.BuildKindProd}
		for k, v := range vals {
			environ = append(environ, k+"="+v)
		}
		if _, err := config.LoadBlob(prefix, environ); err != nil {
			t.Errorf("%s: ключи хранилища не пройдут проверку старта: %v", file, err)
		}
	}
}

// Хранилище своего сервера (план media, Task 12; спека §6.5, R1, R2, R5): сервис storage (Garage) и разовый
// storage-init до процессов; Caddy — домен медиа (только чтение публичного бакета) и адрес хранилища (подписанные
// загрузка и чтение закрытого). Поведение проверяет job server в .github/workflows/image.yml, здесь — файлы.

// garageImage — образ Garage dev-хранилища (scripts/s3.sh, строка IMAGE=): сервер и тесты S3 — одна версия.
func garageImage(t *testing.T) string {
	t.Helper()
	for l := range strings.Lines(readRepo(t, "scripts/s3.sh")) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "IMAGE="); ok {
			return v
		}
	}
	t.Fatal("scripts/s3.sh: нет строки IMAGE=")
	return ""
}

// garageSetting — значение ключа секции [section] deploy/server/storage/garage.toml (строка в кавычках).
func garageSetting(t *testing.T, section, key string) string {
	t.Helper()
	cur := ""
	for l := range strings.Lines(readRepo(t, "deploy/server/storage/garage.toml")) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			cur = strings.Trim(l, "[]")
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if ok && cur == section && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	t.Fatalf("storage/garage.toml: нет %s в [%s]", key, section)
	return ""
}

// garagePort — порт из адреса привязки Garage ("[::]:3900" → "3900").
func garagePort(t *testing.T, section, key string) string {
	t.Helper()
	addr := garageSetting(t, section, key)
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		t.Fatalf("storage/garage.toml: [%s] %s = %q без порта", section, key, addr)
	}
	return addr[i+1:]
}

// storageBuckets — имена бакетов (api.blob.env.example; у admin-api и worker те же — TestServerComposeStorageInit).
func storageBuckets(t *testing.T) (in, pub, priv string) {
	t.Helper()
	v := envFile(t, "deploy/server/api.blob.env.example")
	return v["API_BLOB_S3_BUCKET_IN"], v["API_BLOB_S3_BUCKET_PUB"], v["API_BLOB_S3_BUCKET_PRIV"]
}

func TestServerComposeStorageInit(t *testing.T) {
	raw, err := fs.ReadFile(repoFS, "deploy/server/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]composeService `yaml:"services"`
		Volumes  map[string]any            `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("compose.yaml: %v", err)
	}
	storage, ok := c.Services["storage"]
	if !ok {
		t.Fatal("compose.yaml: нет сервиса storage")
	}
	if want := garageImage(t); storage.Image != want {
		t.Errorf("storage: образ %q, ждали %q (как scripts/s3.sh)", storage.Image, want)
	}
	if storage.Healthcheck == nil || len(storage.Healthcheck.Test) == 0 {
		t.Error("storage: нет healthcheck — storage-init не дождётся узла")
	}
	data := false
	for _, v := range storage.Volumes {
		data = data || strings.HasPrefix(v, "storage_data:")
	}
	if !data {
		t.Errorf("storage: том storage_data не подключён (%q)", storage.Volumes)
	}
	if _, ok := c.Volumes["storage_data"]; !ok {
		t.Error("compose.yaml: том storage_data не объявлен в volumes")
	}
	initSvc, ok := c.Services["storage-init"]
	if !ok {
		t.Fatal("compose.yaml: нет сервиса storage-init")
	}
	if cond := initSvc.DependsOn["storage"].Condition; cond != "service_healthy" {
		t.Errorf("storage-init: depends_on.storage.condition = %q, ждали service_healthy", cond)
	}
	// точный тег: ни latest, ни плавающей линии вроде 8 или 8.23
	for name, img := range map[string]string{"storage": storage.Image, "storage-init": initSvc.Image} {
		ref, _, _ := strings.Cut(img, "@")
		_, tag, ok := strings.Cut(ref, ":")
		if !ok || strings.Count(strings.TrimPrefix(tag, "v"), ".") != 2 {
			t.Errorf("%s: образ %q — нужен точный тег MAJOR.MINOR.PATCH", name, img)
		}
	}
	// сторонний образ init видит секреты хранилища — закреплён по digest, подмена тега не пройдёт (ревью Task 12, m1)
	if _, digest, ok := strings.Cut(initSvc.Image, "@sha256:"); !ok || len(digest) != 64 ||
		strings.Trim(digest, "0123456789abcdef") != "" {
		t.Errorf("storage-init: образ %q — нужен digest @sha256:<64 hex>", initSvc.Image)
	}
	// Спека §6.5: листинг и admin API хранилища наружу не открыты — у storage и storage-init нет опубликованных портов
	// (снаружи — только Caddy; ревью Task 12, m3)
	for name, s := range map[string]composeService{"storage": storage, "storage-init": initSvc} {
		if len(s.Ports) != 0 {
			t.Errorf("%s: опубликованы порты %v — наружу хранилище открывает только Caddy", name, s.Ports)
		}
	}
	// init получает только ключи хранилища: секреты Garage и <сервис>.blob.env, а не env процессов целиком (базы,
	// кольца, SMTP; ревью Task 12, m1)
	if got, want := slices.Sorted(slices.Values(initSvc.EnvFile)),
		[]string{"admin-api.blob.env", "api.blob.env", "storage.env", "worker.blob.env"}; !slices.Equal(got, want) {
		t.Errorf("storage-init: env_file %q, ждали %q", got, want)
	}
	embedded := composeFileServices(t, "deploy/server/compose.ci-embed.yaml")["api"]
	s3 := "http://storage:" + garagePort(t, "s3_api", "api_bind_addr")
	in, pub, priv := storageBuckets(t)
	for svc, prefix := range map[string]string{"api": "API_", "admin-api": "ADMIN_", "worker": "WORKER_"} {
		if cond := c.Services[svc].DependsOn["storage-init"].Condition; cond != "service_completed_successfully" {
			t.Errorf("%s: depends_on.storage-init.condition = %q, ждали service_completed_successfully", svc, cond)
		}
		// процесс читает свои ключи хранилища из того же файла, что init (один источник ключей)
		if got, want := c.Services[svc].EnvFile, []string{svc + ".env", svc + ".blob.env"}; !slices.Equal(got, want) {
			t.Errorf("%s: env_file %q, ждали %q", svc, got, want)
		}
		// встроенный воркер (compose.ci-embed.yaml) — те же файлы api и worker
		if svc != "admin-api" && !slices.Contains(embedded.EnvFile, svc+".blob.env") {
			t.Errorf("compose.ci-embed.yaml: у api нет %s.blob.env в env_file %q", svc, embedded.EnvFile)
		}
		file := svc + ".blob.env.example"
		v := envFile(t, "deploy/server/"+file)
		if got := v[prefix+"BLOB_S3_ENDPOINT"]; got != s3 {
			t.Errorf("%s: %sBLOB_S3_ENDPOINT = %q, ждали внутренний адрес %q", file, prefix, got, s3)
		}
		for k, want := range map[string]string{"IN": in, "PUB": pub, "PRIV": priv} {
			if got := v[prefix+"BLOB_S3_BUCKET_"+k]; got != want || want == "" {
				t.Errorf("%s: %sBLOB_S3_BUCKET_%s = %q, ждали %q (бакеты одни на все процессы)", file,
					prefix, k, got, want)
			}
		}
	}
}

// caddyNode — строка Caddyfile (токены через пробел) и вложенный блок, если строка открывает «{».
type caddyNode struct {
	tokens   []string
	children []*caddyNode
}

// caddySite — блок сайта deploy/server/Caddyfile с адресом addr (разбор строк: «{» в конце открывает блок, «}» —
// закрывает; комментарии — с «#»).
func caddySite(t *testing.T, addr string) *caddyNode {
	t.Helper()
	root := &caddyNode{}
	stack := []*caddyNode{root}
	for l := range strings.Lines(readRepo(t, "deploy/server/Caddyfile")) {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if l == "}" {
			if len(stack) == 1 {
				t.Fatal("Caddyfile: лишняя «}»")
			}
			stack = stack[:len(stack)-1]
			continue
		}
		f := strings.Fields(l)
		n := &caddyNode{tokens: f}
		parent := stack[len(stack)-1]
		parent.children = append(parent.children, n)
		if f[len(f)-1] == "{" {
			n.tokens = f[:len(f)-1]
			stack = append(stack, n)
		}
	}
	if len(stack) != 1 {
		t.Fatal("Caddyfile: незакрытый блок")
	}
	for _, n := range root.children {
		if len(n.tokens) == 1 && n.tokens[0] == addr {
			return n
		}
	}
	t.Fatalf("Caddyfile: нет сайта %s", addr)
	return nil
}

// caddyMatchers — именованные матчеры сайта: имя → строки условий (однострочный «@m path …» или блок «@m { … }»).
func caddyMatchers(site *caddyNode) map[string][][]string {
	out := map[string][][]string{}
	for _, n := range site.children {
		if len(n.tokens) == 0 || !strings.HasPrefix(n.tokens[0], "@") {
			continue
		}
		if len(n.tokens) > 1 {
			out[n.tokens[0]] = append(out[n.tokens[0]], n.tokens[1:])
		}
		for _, c := range n.children {
			out[n.tokens[0]] = append(out[n.tokens[0]], c.tokens)
		}
	}
	return out
}

// caddyProxy — reverse_proxy сайта: матчер (свой или ближайшего handle/route; пусто — без матчера), апстримы, блок.
type caddyProxy struct {
	matcher   string
	upstreams []string
	node      *caddyNode
}

func caddyProxies(site *caddyNode) []caddyProxy {
	var out []caddyProxy
	var walk func(n *caddyNode, matcher string)
	walk = func(n *caddyNode, matcher string) {
		for _, c := range n.children {
			if len(c.tokens) == 0 {
				continue
			}
			m := matcher
			args := c.tokens[1:]
			if len(args) > 0 && strings.HasPrefix(args[0], "@") {
				m = args[0]
				args = args[1:]
			}
			switch c.tokens[0] {
			case "reverse_proxy":
				p := caddyProxy{matcher: m, upstreams: args, node: c}
				for _, s := range c.children {
					if len(s.tokens) > 1 && s.tokens[0] == "to" {
						p.upstreams = append(p.upstreams, s.tokens[1:]...)
					}
				}
				out = append(out, p)
			case "handle", "route":
				walk(c, m)
			}
		}
	}
	walk(site, "")
	return out
}

// caddyFallback — ответ блока handle без матчера (без него необработанный запрос получил бы пустой 200).
func caddyFallback(site *caddyNode) string {
	for _, n := range site.children {
		if len(n.tokens) == 1 && n.tokens[0] == "handle" {
			for _, c := range n.children {
				if len(c.tokens) > 0 && c.tokens[0] == "respond" {
					return strings.Join(c.tokens[1:], " ")
				}
			}
		}
	}
	return ""
}

// matcherLine — аргументы условия матчера с началом kind (например, method) или nil.
func matcherLine(lines [][]string, kind ...string) []string {
	for _, l := range lines {
		if len(l) >= len(kind) && slices.Equal(l[:len(kind)], kind) {
			return l[len(kind):]
		}
	}
	return nil
}

// Спека §6.5 (I-7), R5: домен медиа (может стоять за CDN) — только GET/HEAD к публичному бакету через сайт Garage;
// корень и пути входящего и закрытого бакетов не проксируются ни одним методом; прочее — 405; nosniff.
func TestServerCaddyMediaDomainReadOnly(t *testing.T) {
	site := caddySite(t, "{$WF_MEDIA_DOMAIN}")
	in, pub, priv := storageBuckets(t)
	nosniff := false
	for _, n := range site.children {
		nosniff = nosniff || slices.Equal(n.tokens, []string{"header", "X-Content-Type-Options", "nosniff"})
	}
	if !nosniff {
		t.Error("домен медиа: нет «header X-Content-Type-Options nosniff» на уровне сайта")
	}
	ms := caddyMatchers(site)
	proxies := caddyProxies(site)
	if len(proxies) == 0 {
		t.Fatal("домен медиа: нет reverse_proxy")
	}
	web := "http://storage:" + garagePort(t, "s3_web", "bind_addr")
	host := pub + garageSetting(t, "s3_web", "root_domain")
	for _, p := range proxies {
		if p.matcher == "" {
			t.Error("домен медиа: reverse_proxy без матчера — проксировался бы любой метод и путь")
			continue
		}
		if m := matcherLine(ms[p.matcher], "method"); !slices.Equal(m, []string{"GET", "HEAD"}) {
			t.Errorf("домен медиа: матчер %s — method %q, ждали ровно GET HEAD", p.matcher, m)
		}
		hidden := matcherLine(ms[p.matcher], "not", "path")
		for _, want := range []string{"/", "/" + in, "/" + in + "/*", "/" + priv, "/" + priv + "/*"} {
			if !slices.Contains(hidden, want) {
				t.Errorf("домен медиа: матчер %s не исключает путь %s — он ушёл бы в сайт бакета", p.matcher, want)
			}
		}
		if !slices.Equal(p.upstreams, []string{web}) {
			t.Errorf("домен медиа: апстрим %q, ждали web-порт Garage %s", p.upstreams, web)
		}
		var hostUp []string
		for _, c := range p.node.children {
			if len(c.tokens) > 1 && c.tokens[0] == "header_up" && strings.EqualFold(c.tokens[1], "Host") {
				hostUp = c.tokens[2:]
			}
		}
		if !slices.Equal(hostUp, []string{host}) {
			t.Errorf("домен медиа: header_up Host %q, ждали сайт бакета %s", hostUp, host)
		}
	}
	if fb := caddyFallback(site); fb != "405" {
		t.Errorf("домен медиа: handle без матчера отвечает %q, ждали 405", fb)
	}
}

// R5, R9: адрес хранилища (без CDN) — подпись SigV4 включает Host, поэтому Caddy его не подменяет (Caddy ≥ 2.11
// подменяет Host только у HTTPS-апстрима — апстрим внутри сети по http://); наружу — только подписанная загрузка POST
// в бакет входящих (и OPTIONS CORS) и подписанное чтение GET/HEAD закрытого; листинг и прочее — 404.
func TestServerCaddyStoragePreservesHost(t *testing.T) {
	site := caddySite(t, "{$WF_STORAGE_DOMAIN}")
	in, _, priv := storageBuckets(t)
	var walk func(n *caddyNode)
	walk = func(n *caddyNode) {
		for _, c := range n.children {
			if len(c.tokens) > 1 && c.tokens[0] == "header_up" &&
				strings.EqualFold(strings.TrimLeft(c.tokens[1], "+-"), "Host") {
				t.Errorf("адрес хранилища: %q — подмена Host ломает подпись SigV4", strings.Join(c.tokens, " "))
			}
			walk(c)
		}
	}
	walk(site)
	ms := caddyMatchers(site)
	proxies := caddyProxies(site)
	if len(proxies) == 0 {
		t.Fatal("адрес хранилища: нет reverse_proxy")
	}
	s3 := "http://storage:" + garagePort(t, "s3_api", "api_bind_addr")
	seen := map[string]bool{}
	for _, p := range proxies {
		if !slices.Equal(p.upstreams, []string{s3}) {
			t.Errorf("адрес хранилища: апстрим %q, ждали S3-порт Garage по http:// %s", p.upstreams, s3)
		}
		lines := ms[p.matcher]
		path, method := matcherLine(lines, "path"), matcherLine(lines, "method")
		switch {
		case p.matcher == "":
			t.Error("адрес хранилища: reverse_proxy без матчера — наружу ушли бы листинг и любые методы")
		case slices.Equal(path, []string{"/" + in}) && slices.Equal(method, []string{"POST", "OPTIONS"}):
			seen["upload"] = true
		case slices.Equal(path, []string{"/" + priv + "/*"}) && slices.Equal(method, []string{"GET", "HEAD"}) &&
			slices.Equal(matcherLine(lines, "query"), []string{"X-Amz-Signature=*"}):
			seen["read"] = true
		default:
			t.Errorf("адрес хранилища: reverse_proxy под матчером %s (%q) — разрешены только POST/OPTIONS /%s и "+
				"подписанные GET/HEAD /%s/*", p.matcher, lines, in, priv)
		}
	}
	if !seen["upload"] || !seen["read"] {
		t.Errorf("адрес хранилища: нет маршрута загрузки (%v) или чтения закрытого (%v)", seen["upload"], seen["read"])
	}
	if fb := caddyFallback(site); fb != "404" {
		t.Errorf("адрес хранилища: handle без матчера отвечает %q, ждали 404", fb)
	}
}
