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

	"go.yaml.in/yaml/v3"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/peer"
)

// renderEnv — переменные wf-api из render.yaml (значения, заданные в файле, а не в панели Render).
func renderEnv(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("../../../../render.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var bp struct {
		Services []struct {
			Name    string `yaml:"name"`
			EnvVars []struct {
				Key   string `yaml:"key"`
				Value string `yaml:"value"`
			} `yaml:"envVars"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &bp); err != nil {
		t.Fatal(err)
	}
	for _, s := range bp.Services {
		if s.Name != "wf-api" {
			continue
		}
		env := map[string]string{}
		for _, e := range s.EnvVars {
			env[e.Key] = e.Value
		}
		return env
	}
	t.Fatal("в render.yaml нет сервиса wf-api")
	return nil
}

// Образ wf-api — с собранной админкой (план C, Task 1, R1): контекст сборки — корень репозитория (Node-стадия
// берёт воркспейс pnpm), Dockerfile — backend/Dockerfile, а деплой запускается правками и бэкенда, и админки с её
// зависимостями. Корневой контекст без .dockerignore потащил бы node_modules, .tools/pg и .env в слои образа.
func TestRenderYAMLBuild(t *testing.T) {
	raw, err := os.ReadFile("../../../../render.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var bp struct {
		Services []struct {
			Name           string `yaml:"name"`
			DockerfilePath string `yaml:"dockerfilePath"`
			DockerContext  string `yaml:"dockerContext"`
			BuildFilter    struct {
				Paths []string `yaml:"paths"`
			} `yaml:"buildFilter"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &bp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range bp.Services {
		if s.Name != "wf-api" {
			continue
		}
		found = true
		if s.DockerContext != "." {
			t.Errorf("wf-api: dockerContext %q, ждали «.» (корень: стадия админки)", s.DockerContext)
		}
		if s.DockerfilePath != "./backend/Dockerfile" {
			t.Errorf("wf-api: dockerfilePath %q, ждали ./backend/Dockerfile", s.DockerfilePath)
		}
		paths := map[string]bool{}
		for _, p := range s.BuildFilter.Paths {
			paths[p] = true
		}
		for _, want := range []string{"backend/**", "apps/admin/**", "packages/**", "contracts/package.json",
			"contracts/src/**", "apps/web/package.json", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml",
			".dockerignore", "scripts/check-admin-dist.mjs", "render.yaml"} {
			if !paths[want] {
				t.Errorf("wf-api: в buildFilter.paths нет %q — правка не пересоберёт образ", want)
			}
		}
		// проверка образа в CI (image.yml) запускается на каждую правку, которая пересобирает образ на Render
		imagePaths := workflowPaths(t, ".github/workflows/image.yml")
		for _, p := range s.BuildFilter.Paths {
			if !imagePaths[p] {
				t.Errorf("image.yml: в paths нет %q из buildFilter wf-api — Render соберёт образ, который CI не проверял", p)
			}
		}
	}
	if !found {
		t.Fatal("в render.yaml нет сервиса wf-api")
	}

	// .dockerignore корня — список разрешённого: всё закрыто, открыто нужное сборке, внутри снова закрыты
	// зависимости, сборки и секреты
	raw, err = os.ReadFile("../../../../.dockerignore")
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
