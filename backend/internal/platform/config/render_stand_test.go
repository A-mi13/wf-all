package config_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
	"go.yaml.in/yaml/v3"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/peer"
)

// envVar — переменная сервиса Blueprint: значение в файле (value), сгенерированное Render (generateValue) или
// заданное в панели (sync: false).
type envVar struct {
	Key           string  `yaml:"key"`
	Value         *string `yaml:"value"`
	GenerateValue bool    `yaml:"generateValue"`
	Sync          *bool   `yaml:"sync"`
}

type renderService struct {
	Type           string   `yaml:"type"`
	Name           string   `yaml:"name"`
	Runtime        string   `yaml:"runtime"`
	Plan           string   `yaml:"plan"`
	Region         string   `yaml:"region"`
	Branch         string   `yaml:"branch"`
	DockerfilePath string   `yaml:"dockerfilePath"`
	DockerContext  string   `yaml:"dockerContext"`
	DockerCommand  string   `yaml:"dockerCommand"`
	HealthCheck    string   `yaml:"healthCheckPath"`
	AutoDeploy     string   `yaml:"autoDeployTrigger"`
	MaxShutdown    *int     `yaml:"maxShutdownDelaySeconds"`
	EnvVars        []envVar `yaml:"envVars"`
	BuildFilter    struct {
		Paths []string `yaml:"paths"`
	} `yaml:"buildFilter"`
}

// blueprint — сервисы render.yaml.
func blueprint(t *testing.T) []renderService {
	t.Helper()
	raw, err := fs.ReadFile(repoFS, "render.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var bp struct {
		Services []renderService `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &bp); err != nil {
		t.Fatal(err)
	}
	return bp.Services
}

// service — сервис Blueprint по имени.
func service(t *testing.T, name string) renderService {
	t.Helper()
	for _, s := range blueprint(t) {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("в render.yaml нет сервиса %s", name)
	return renderService{}
}

// fileEnv — переменные сервиса со значением в файле (без sync: false и generateValue).
func (s renderService) fileEnv() map[string]string {
	env := map[string]string{}
	for _, e := range s.EnvVars {
		if e.Value != nil {
			env[e.Key] = *e.Value
		}
	}
	return env
}

// environ — окружение процесса сервиса: значения из файла плюс заглушки для переменных из панели (sync: false) и
// сгенерированных Render; заглушка — из fill по ключу, иначе ошибка теста (новая переменная без заглушки).
func (s renderService) environ(t *testing.T, fill map[string]string) []string {
	t.Helper()
	var out []string
	for _, e := range s.EnvVars {
		switch {
		case e.Value != nil:
			out = append(out, e.Key+"="+*e.Value)
		default:
			v, ok := fill[e.Key]
			if !ok {
				t.Fatalf("%s: для %s (sync: false / generateValue) нет заглушки в тесте", s.Name, e.Key)
			}
			out = append(out, e.Key+"="+v)
		}
	}
	return out
}

// renderEnv — переменные wf-api из render.yaml (значения, заданные в файле, а не в панели Render).
func renderEnv(t *testing.T) map[string]string {
	t.Helper()
	return service(t, "wf-api").fileEnv()
}

// key32 — годный ключ в 32 нулевых байта (base64): заглушка секретов панели, gitleaks не примет за секрет.
var key32 = strings.Repeat("A", 43) + "="

// adminFill — заглушки переменных wf-admin-api, которые задаются в панели Render (sync: false).
var adminFill = map[string]string{
	"ADMIN_DATABASE_URL":       "postgres://admin@db.example/wf?sslmode=require",
	"ADMIN_STAFF_SECRET_KEYS":  key32,
	"ADMIN_ORIGIN":             "https://wf-admin-api.onrender.com",
	"ADMIN_MAIL_SMTP_ADDR":     "smtp.resend.com:2587",
	"ADMIN_MAIL_FROM":          "WF <noreply@wf.example>",
	"ADMIN_MAIL_SMTP_USERNAME": "resend",
	"ADMIN_MAIL_SMTP_PASSWORD": "x",
}

// adminConfig — конфигурация admin-api так, как её соберёт процесс на стенде.
func adminConfig(t *testing.T) config.Admin {
	t.Helper()
	c, err := config.Load[config.Admin]("ADMIN_", service(t, "wf-admin-api").environ(t, adminFill))
	if err != nil {
		t.Fatalf("render.yaml: настройки wf-admin-api не разбираются: %v", err)
	}
	return c
}

// Образ wf-api и wf-admin-api — один, с собранной админкой (план C, Task 1, R1; Task 5): контекст сборки — корень
// репозитория (Node-стадия берёт воркспейс pnpm), Dockerfile — backend/Dockerfile, а деплой запускается правками и
// бэкенда, и админки с её зависимостями. Корневой контекст без .dockerignore потащил бы node_modules, .tools/pg и
// .env в слои образа.
func TestRenderYAMLBuild(t *testing.T) {
	imagePaths := workflowPaths(t, ".github/workflows/image.yml")
	for _, name := range []string{"wf-api", "wf-admin-api"} {
		s := service(t, name)
		if s.Runtime != "docker" {
			t.Errorf("%s: runtime %q, ждали docker", name, s.Runtime)
		}
		if s.DockerContext != "." {
			t.Errorf("%s: dockerContext %q, ждали «.» (корень: стадия админки)", name, s.DockerContext)
		}
		if s.DockerfilePath != "./backend/Dockerfile" {
			t.Errorf("%s: dockerfilePath %q, ждали ./backend/Dockerfile", name, s.DockerfilePath)
		}
		paths := map[string]bool{}
		for _, p := range s.BuildFilter.Paths {
			paths[p] = true
		}
		for _, want := range []string{"backend/**", "apps/admin/**", "packages/**", "contracts/package.json",
			"contracts/src/**", "apps/web/package.json", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml",
			".dockerignore", "scripts/check-admin-dist.mjs", "render.yaml"} {
			if !paths[want] {
				t.Errorf("%s: в buildFilter.paths нет %q — правка не пересоберёт образ", name, want)
			}
		}
		// проверка образа в CI (image.yml) запускается на каждую правку, которая пересобирает образ на Render
		for _, p := range s.BuildFilter.Paths {
			if !imagePaths[p] {
				t.Errorf("image.yml: в paths нет %q из buildFilter %s — Render соберёт образ, который CI не проверял", p, name)
			}
		}
	}

	// .dockerignore корня — список разрешённого: всё закрыто, открыто нужное сборке, внутри снова закрыты
	// зависимости, сборки и секреты
	raw, err := fs.ReadFile(repoFS, ".dockerignore")
	if err != nil {
		t.Fatalf("нет .dockerignore в корне — контекст сборки потащит node_modules и .tools: %v", err)
	}
	ignore := parseDockerignore(string(raw))
	lines := map[string]bool{}
	for _, r := range ignore {
		lines[r.line] = true
	}
	for _, want := range []string{"*", "**/node_modules", "**/.env", "**/.env.*", "**/dist"} {
		if !lines[want] {
			t.Errorf(".dockerignore: нет строки %q", want)
		}
	}

	// Пакеты воркспейса, от которых зависит админка, — в контексте вместе с файлами своих exports: tsc сборки
	// компилирует src целиком (и тесты), а тест src/i18n берёт типы @wf/contracts/* (ревью C1, I1: в контексте был
	// только манифест contracts — TS2307 в стадии admin).
	for _, file := range workspaceExportFiles(t) {
		if !ignore.included(file) {
			t.Errorf(".dockerignore: %s (exports пакета воркспейса, нужен сборке админки) не в контексте", file)
		}
	}
}

// repoFS — корень репозитория (тест идёт из backend/internal/platform/config).
var repoFS = os.DirFS("../../../..")

// workflowPaths — on.push.paths workflow GitHub Actions (путь от корня репозитория).
func workflowPaths(t *testing.T, file string) map[string]bool {
	t.Helper()
	raw, err := fs.ReadFile(repoFS, file)
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On struct {
			Push struct {
				Paths []string `yaml:"paths"`
			} `yaml:"push"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, p := range wf.On.Push.Paths {
		out[p] = true
	}
	if len(out) == 0 {
		t.Fatalf("%s: нет on.push.paths", file)
	}
	return out
}

type ignoreRule struct {
	line   string // как в файле
	negate bool
	pat    string
}

type dockerignore []ignoreRule

func parseDockerignore(s string) dockerignore {
	var out dockerignore
	for l := range strings.Lines(s) {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		r := ignoreRule{line: l, pat: l}
		if strings.HasPrefix(l, "!") {
			r.negate, r.pat = true, l[1:]
		}
		r.pat = strings.TrimSuffix(r.pat, "/")
		out = append(out, r)
	}
	return out
}

// included — попадёт ли файл (путь от корня, через «/») в контекст: последнее совпавшее правило решает, как у
// Docker. Поддержано то, что есть в нашем файле: «*», точный путь или каталог-префикс, «**/имя» (имя — шаблон
// path.Match для любого сегмента пути).
func (d dockerignore) included(file string) bool {
	in := true
	for _, r := range d {
		if r.matches(file) {
			in = r.negate
		}
	}
	return in
}

func (r ignoreRule) matches(file string) bool {
	if name, ok := strings.CutPrefix(r.pat, "**/"); ok {
		for seg := range strings.SplitSeq(file, "/") {
			if m, _ := path.Match(name, seg); m {
				return true
			}
		}
		return false
	}
	if r.pat == "*" {
		return true
	}
	return file == r.pat || strings.HasPrefix(file, r.pat+"/")
}

// workspaceExportFiles — файлы exports (и манифесты) пакетов воркспейса из зависимостей apps/admin.
func workspaceExportFiles(t *testing.T) []string {
	t.Helper()
	// пути — от корня репозитория (repoFS)
	type manifest struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Exports         any               `json:"exports"`
	}
	read := func(dir string) manifest {
		raw, err := fs.ReadFile(repoFS, dir+"/package.json")
		if err != nil {
			t.Fatal(err)
		}
		var m manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s/package.json: %v", dir, err)
		}
		return m
	}
	dirs := map[string]string{} // имя пакета → каталог
	for _, glob := range []string{"apps/*", "packages/*"} {
		ms, err := fs.Glob(repoFS, glob+"/package.json")
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			dir := path.Dir(m)
			dirs[read(dir).Name] = dir
		}
	}
	dirs[read("contracts").Name] = "contracts"

	admin := read("apps/admin")
	var files []string
	for _, deps := range []map[string]string{admin.Dependencies, admin.DevDependencies} {
		for name, spec := range deps {
			if !strings.HasPrefix(spec, "workspace:") {
				continue
			}
			dir, ok := dirs[name]
			if !ok {
				t.Fatalf("apps/admin: пакет воркспейса %s не найден", name)
			}
			files = append(files, dir+"/package.json")
			for _, f := range exportTargets(read(dir).Exports) {
				files = append(files, path.Clean(dir+"/"+f))
			}
		}
	}
	if len(files) == 0 {
		t.Fatal("apps/admin: нет зависимостей воркспейса — разбор package.json сломан")
	}
	return files
}

// exportTargets — строки-листья поля exports («./x.ts», {"types": …, "default": …}).
func exportTargets(v any) []string {
	switch e := v.(type) {
	case string:
		return []string{e}
	case map[string]any:
		var out []string
		for _, sub := range e {
			out = append(out, exportTargets(sub)...)
		}
		return out
	}
	return nil
}

// Страж исправления стенда: на бесплатном тарифе Render к приложению подключается локальный прокси
// по петле ([::1], лог peer 02.10.2026), и пока петли не было в API_TRUSTED_PROXIES, заголовок CDN
// не читался — все клиенты получали IP ::1 (общий лимит). Берём настройки так, как они лежат в
// render.yaml, и прогоняем ту же картину запроса.
func TestRenderYAMLClientIP(t *testing.T) {
	stand := renderEnv(t)
	for _, k := range []string{"API_TRUSTED_PROXIES", "API_CLIENT_IP_HEADER"} {
		if v, ok := stand[k]; !ok || v == "" {
			t.Fatalf("render.yaml: у wf-api нет %s со значением", k)
		}
	}
	c, err := config.Load[config.API]("API_", []string{
		"API_HTTP_ADDR=:8080", "API_DATABASE_URL=postgres://x", "API_JWT_SEEDS=a", "API_HUMANCHECK_KEYS=k",
		"API_TRUSTED_PROXIES=" + stand["API_TRUSTED_PROXIES"],
		"API_CLIENT_IP_HEADER=" + stand["API_CLIENT_IP_HEADER"],
	})
	if err != nil {
		t.Fatalf("render.yaml: настройки не разбираются: %v", err)
	}
	pc := peer.Config{TrustedProxies: c.Peer.TrustedProxies, ClientIPHeader: c.Peer.ClientIPHeader}
	if err := pc.Validate(); err != nil {
		t.Fatalf("render.yaml: peer.Config невалиден: %v", err)
	}

	var got peer.Info
	h := peer.Middleware(pc)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = peer.From(r.Context()) }))
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:41198"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 172.70.242.31, 10.29.109.133")
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if want := netip.MustParseAddr("203.0.113.7"); got.IP != want {
		t.Fatalf("клиент %v, ждали %v: петля [::1] не в API_TRUSTED_PROXIES из render.yaml (%q)",
			got.IP, want, stand["API_TRUSTED_PROXIES"])
	}
}

// --- wf-admin-api (план C, Task 5) ---

// Сервис admin-api на бесплатном тарифе (Q3): тот же образ, команда admin-api; окно остановки не задаётся (R4 —
// умолчание Render 30 с, config.StopWindow); воркера в Blueprint нет (R9).
func TestRenderYAMLAdminService(t *testing.T) {
	s := service(t, "wf-admin-api")
	for _, c := range []struct{ field, got, want string }{
		{"type", s.Type, "web"},
		{"plan", s.Plan, "free"},
		{"region", s.Region, "frankfurt"},
		{"branch", s.Branch, "main"},
		{"dockerCommand", s.DockerCommand, "admin-api"},
		{"healthCheckPath", s.HealthCheck, "/healthz"},
		{"autoDeployTrigger", s.AutoDeploy, "checksPass"},
	} {
		if c.got != c.want {
			t.Errorf("wf-admin-api: %s = %q, ждали %q", c.field, c.got, c.want)
		}
	}
	// Render направляет трафик на PORT (по умолчанию 10000) всех интерфейсов
	if got := s.fileEnv()["ADMIN_HTTP_ADDR"]; got != "0.0.0.0:10000" {
		t.Errorf("ADMIN_HTTP_ADDR = %q, ждали 0.0.0.0:10000", got)
	}
	if got := s.fileEnv()["ADMIN_LOG_FORMAT"]; got != "json" {
		t.Errorf("ADMIN_LOG_FORMAT = %q, ждали json", got)
	}
}

// Доверие к прокси admin-api — как у wf-api (петля локального прокси Render free и частные сети): запрос с петли с
// CF-Connecting-IP даёт IP посетителя; без петли все сотрудники получили бы ::1 — общий лимит входа.
func TestRenderYAMLAdminClientIP(t *testing.T) {
	c := adminConfig(t)
	env := service(t, "wf-admin-api").fileEnv()
	if api := renderEnv(t)["API_TRUSTED_PROXIES"]; env["ADMIN_TRUSTED_PROXIES"] != api {
		t.Errorf("ADMIN_TRUSTED_PROXIES %q ≠ API_TRUSTED_PROXIES %q — цепочка прокси у сервисов одна",
			env["ADMIN_TRUSTED_PROXIES"], api)
	}
	pc := peer.Config{TrustedProxies: c.TrustedProxies, ClientIPHeader: c.ClientIPHeader}
	if err := pc.Validate(); err != nil {
		t.Fatalf("render.yaml: peer.Config admin-api невалиден: %v", err)
	}
	var got peer.Info
	h := peer.Middleware(pc)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = peer.From(r.Context()) }))
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:41198"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 172.70.242.31, 10.29.109.133")
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if want := netip.MustParseAddr("203.0.113.7"); got.IP != want {
		t.Fatalf("клиент %v, ждали %v: петля [::1] не в ADMIN_TRUSTED_PROXIES (%q)", got.IP, want, env["ADMIN_TRUSTED_PROXIES"])
	}
}

// Cookie только Secure; аллоулист выключен (R6: только с закрытым origin), Swagger выключен (R5); письма — только по
// TLS (R8). Значения заданы в файле явно, а не умолчаниями: смена умолчания в коде стенд не меняет.
func TestRenderYAMLAdminCookie(t *testing.T) {
	env := service(t, "wf-admin-api").fileEnv()
	for k, want := range map[string]string{
		"ADMIN_COOKIE_SECURE": "true", "ADMIN_DOCS_ENABLED": "false", "ADMIN_MAIL_SMTP_TLS": "mandatory",
		"ADMIN_CLIENT_IP_HEADER": "CF-Connecting-IP",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, ждали %q", k, env[k], want)
		}
	}
	for _, e := range service(t, "wf-admin-api").EnvVars {
		if e.Key == "ADMIN_IP_ALLOWLIST" {
			t.Error("ADMIN_IP_ALLOWLIST задан — на стенде аллоулист выключен (R6)")
		}
	}
	c := adminConfig(t)
	if !c.CookieSecure || c.HTTP.DocsEnabled || len(c.IPAllowlist) != 0 || c.Mail.TLS != "mandatory" {
		t.Errorf("admin-api на стенде: CookieSecure=%v DocsEnabled=%v IPAllowlist=%v TLS=%q",
			c.CookieSecure, c.HTTP.DocsEnabled, c.IPAllowlist, c.Mail.TLS)
	}
}

// Секреты и адреса, известные только после создания сервиса, — в панели Render (sync: false), не в файле и не
// generateValue: ключи сотрудников нужны и командам оператора (R2), сгенерированный Render ключ пришлось бы копировать
// из панели, а перегенерация обнулила бы все секреты TOTP. У каждой переменной — ровно один источник.
func TestRenderYAMLSecretsNotInFile(t *testing.T) {
	secret := []string{"DATABASE_URL", "SECRET", "PASSWORD", "USERNAME", "SMTP_ADDR", "MAIL_FROM", "ORIGIN"}
	for _, s := range blueprint(t) {
		for _, e := range s.EnvVars {
			n := 0
			if e.Value != nil {
				n++
			}
			if e.GenerateValue {
				n++
			}
			if e.Sync != nil {
				if *e.Sync {
					t.Errorf("%s: %s — sync: true не используется", s.Name, e.Key)
				}
				n++
			}
			if n != 1 {
				t.Errorf("%s: %s — нужен ровно один из value, generateValue, sync: false", s.Name, e.Key)
			}
			for _, w := range secret {
				if strings.Contains(e.Key, w) && (e.Sync == nil || e.Value != nil || e.GenerateValue) {
					t.Errorf("%s: %s — секрет или адрес стенда, только sync: false", s.Name, e.Key)
				}
			}
		}
	}
	panel := map[string]bool{}
	for _, e := range service(t, "wf-admin-api").EnvVars {
		if e.Sync != nil && !*e.Sync {
			panel[e.Key] = true
		}
	}
	for k := range adminFill {
		if !panel[k] {
			t.Errorf("wf-admin-api: %s не задан как sync: false", k)
		}
	}
}

// Каталог статики — тот, куда Dockerfile кладёт собранную админку (R1: в образе ADMIN_STATIC_DIR не задан).
func TestRenderYAMLStaticDir(t *testing.T) {
	raw, err := fs.ReadFile(repoFS, "backend/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dst := ""
	for l := range strings.Lines(string(raw)) {
		if f := strings.Fields(l); len(f) == 4 && f[0] == "COPY" && f[1] == "--from=admin" {
			dst = strings.TrimSuffix(f[3], "/")
		}
		if strings.HasPrefix(strings.TrimSpace(l), "ENV ") && strings.Contains(l, "ADMIN_STATIC_DIR") {
			t.Error("Dockerfile задаёт ADMIN_STATIC_DIR — каталог задаёт сервис (R1)")
		}
	}
	if dst == "" {
		t.Fatal("в Dockerfile нет COPY --from=admin <dist> <каталог>")
	}
	if got := service(t, "wf-admin-api").fileEnv()["ADMIN_STATIC_DIR"]; got != dst {
		t.Errorf("ADMIN_STATIC_DIR = %q, а Dockerfile кладёт сборку в %q", got, dst)
	}
}

// Воркера на Render нет (ответ Q1, R9): импорт geo — командой оператора. Подняли воркер — тест правится вместе с
// решением пользователя.
func TestRenderYAMLNoWorker(t *testing.T) {
	for _, s := range blueprint(t) {
		if s.Type == "worker" || strings.Contains(s.DockerCommand, "worker") || strings.Contains(s.Name, "worker") {
			t.Errorf("сервис %s (%s, %q) — воркер на Render не заводится (R9)", s.Name, s.Type, s.DockerCommand)
		}
	}
}

// Остановка укладывается в окно Render (R4): admin-api — дренаж ADMIN_SHUTDOWN_TIMEOUT стенда + мягкая и жёсткая
// остановка River ≤ config.StopBudget; wf-api — только дренаж HTTP. Окно не переопределено
// (maxShutdownDelaySeconds), значит, оно — config.StopWindow.
func TestRenderYAMLStopBudget(t *testing.T) {
	for _, s := range blueprint(t) {
		if s.MaxShutdown != nil {
			t.Errorf("%s: maxShutdownDelaySeconds задан — окно стенда считается умолчанием Render (R4)", s.Name)
		}
	}
	if _, ok := service(t, "wf-admin-api").fileEnv()["ADMIN_SHUTDOWN_TIMEOUT"]; !ok {
		t.Error("ADMIN_SHUTDOWN_TIMEOUT не задан в render.yaml — умолчание 10 с не укладывается в бюджет")
	}
	c := adminConfig(t)
	if st := config.AdminStopTime(c.HTTP.ShutdownTimeout); st > config.StopBudget {
		t.Errorf("остановка admin-api на стенде %s > бюджета %s (дренаж %s)", st, config.StopBudget, c.HTTP.ShutdownTimeout)
	}
	if d := apiConfig(t).HTTP.ShutdownTimeout; d > config.StopBudget {
		t.Errorf("дренаж wf-api %s > бюджета %s", d, config.StopBudget)
	}
}

// renderHealthDeployWait — сколько Render ждёт успешной проверки здоровья нового инстанса при деплое
// (render.com/docs/health-checks, 13.11.2026: 15 минут, затем деплой отменяется).
const renderHealthDeployWait = 15 * time.Minute

// admin-api ждёт схему (R3) меньше, чем Render ждёт его здоровья: иначе деплой отменится раньше понятной ошибки.
func TestRenderYAMLSchemaWait(t *testing.T) {
	if _, ok := service(t, "wf-admin-api").fileEnv()["ADMIN_DB_SCHEMA_WAIT"]; !ok {
		t.Error("ADMIN_DB_SCHEMA_WAIT не задан в render.yaml")
	}
	if w := adminConfig(t).DB.SchemaWait; w <= 0 || w >= renderHealthDeployWait {
		t.Errorf("ADMIN_DB_SCHEMA_WAIT %s — нужно (0; %s)", w, renderHealthDeployWait)
	}
}

// Потолок соединений Neon wf-dev: из SHOW max_connections Neon, 13.11.2026 — 112, из них
// superuser_reserved_connections — 4 (данные контроллера плана C).
const (
	neonMaxConnections    = 112
	neonSuperuserReserved = 4
	// operatorCommandsReserve — команды оператора со своей машины (admin-api staff …, worker geo import) с пулами по
	// умолчанию config.DB (10 + 10)
	operatorCommandsReserve = 20
)

// apiFill — заглушки переменных wf-api из панели и generateValue.
var apiFill = map[string]string{
	"API_JWT_SEEDS": key32, "API_HUMANCHECK_KEYS": key32,
	"API_DATABASE_URL":      "postgres://api@db.example/wf?sslmode=require",
	"MIGRATOR_DATABASE_URL": "postgres://wf_owner@db.example/wf?sslmode=require",
}

// apiConfig, migratorConfig — wf-api и его migrate (migrate-then-api) так, как их соберёт процесс на стенде.
func apiConfig(t *testing.T) config.API {
	t.Helper()
	c, err := config.Load[config.API]("API_", service(t, "wf-api").environ(t, apiFill))
	if err != nil {
		t.Fatalf("render.yaml: настройки wf-api не разбираются: %v", err)
	}
	return c
}

func migratorConfig(t *testing.T) config.Migrator {
	t.Helper()
	c, err := config.Load[config.Migrator]("MIGRATOR_", service(t, "wf-api").environ(t, apiFill))
	if err != nil {
		t.Fatalf("render.yaml: настройки мигратора wf-api не разбираются: %v", err)
	}
	return c
}

// Пулы сервисов стенда (R4) с запасом на команды оператора — в пределах соединений Neon, доступных не
// суперпользователю. Мигратор wf-api учтён целиком, хотя его соединения закрыты до старта api: так проще и с запасом.
func TestRenderYAMLPools(t *testing.T) {
	if _, ok := service(t, "wf-admin-api").fileEnv()["ADMIN_DB_MAX_CONNS"]; !ok {
		t.Error("ADMIN_DB_MAX_CONNS не задан в render.yaml")
	}
	pools := map[string]int32{
		"wf-api":         apiConfig(t).DB.MaxConns,
		"wf-api migrate": migratorConfig(t).DB.MaxConns,
		"wf-admin-api":   adminConfig(t).DB.MaxConns,
	}
	sum := int32(operatorCommandsReserve)
	for name, n := range pools {
		if n < 1 {
			t.Errorf("%s: пул %d", name, n)
		}
		sum += n
	}
	if limit := int32(neonMaxConnections - neonSuperuserReserved); sum > limit {
		t.Errorf("пулы стенда %v + команды оператора %d = %d > %d соединений Neon", pools, operatorCommandsReserve, sum, limit)
	}
	// все сервисы Blueprint учтены: новый сервис с базой — строка в pools
	for _, s := range blueprint(t) {
		if _, ok := pools[s.Name]; !ok {
			t.Errorf("сервис %s не учтён в сумме пулов", s.Name)
		}
	}
}

// Каждая переменная Blueprint читается своим процессом: опечатка в ключе (ADMIN_SHUTDOWN_TIMOUT) молча оставила бы
// умолчание. Ключи — из тегов env структур config с префиксом процесса.
func TestRenderYAMLKnownKeys(t *testing.T) {
	known := map[string]bool{}
	add := func(v any, prefix string) {
		ps, err := env.GetFieldParamsWithOptions(v, env.Options{Prefix: prefix})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			known[p.Key] = true
		}
	}
	add(&config.API{}, "API_")
	add(&config.Migrator{}, "MIGRATOR_")
	add(&config.Admin{}, "ADMIN_")
	if !known["ADMIN_MAIL_SMTP_TLS"] || !known["API_TRUSTED_PROXIES"] {
		t.Fatal("разбор тегов env не дал ожидаемых ключей")
	}
	for _, s := range blueprint(t) {
		for _, e := range s.EnvVars {
			if !known[e.Key] {
				t.Errorf("%s: %s — такого ключа нет в config (опечатка?)", s.Name, e.Key)
			}
		}
	}
}
