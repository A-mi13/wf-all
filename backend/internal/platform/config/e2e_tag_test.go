package config_test

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"wf/backend/internal/platform/config"
)

// e2eTag — тег сборки тестовых портов условий капитанства (cmd/{api,admin-api}/captaincy_e2e.go, спека identity §10.3,
// К1): с ним оба условия «выполнены», без него — «проверить нельзя».
const e2eTag = "wf_e2e"

func readRepo(t *testing.T, name string) string {
	t.Helper()
	raw, err := fs.ReadFile(repoFS, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Ревью W1 I-4, m5: тег тестовых портов — только в бинарниках живого e2e (scripts/e2e-admin.sh). Прод-образ
// (backend/Dockerfile), стенд Render (render.yaml), проверка образа в CI (image.yml) и свой сервер (deploy/server)
// собираются и запускаются без него: тег там — обход правила капитанства в проде. TestCaptainChecksProdBuild проверяет
// сборку без тега, а не то, что образ собирается без тега.
func TestProdBuildWithoutE2ETag(t *testing.T) {
	files := []string{"backend/Dockerfile", "render.yaml", ".github/workflows/image.yml"}
	server, err := fs.ReadDir(repoFS, "deploy/server")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range server {
		if !e.IsDir() {
			files = append(files, path.Join("deploy/server", e.Name()))
		}
	}
	for _, f := range files {
		if strings.Contains(readRepo(t, f), e2eTag) {
			t.Errorf("%s: упомянут тег %s — прод-сборка получила бы тестовые порты капитанства", f, e2eTag)
		}
	}
	// образ собирает бинарники сам и без тегов вообще: ни -tags, ни GOFLAGS (через них тег пришёл бы под другим
	// написанием); сборки нет — проверка вхолостую
	docker := readRepo(t, "backend/Dockerfile")
	if !strings.Contains(docker, "go build") {
		t.Fatal("backend/Dockerfile: нет go build — страж проверяет не тот файл")
	}
	for _, bad := range []string{"-tags", "GOFLAGS"} {
		if strings.Contains(docker, bad) {
			t.Errorf("backend/Dockerfile: %s в сборке образа — теги прод-бинарников запрещены", bad)
		}
	}
	// имя тега то же, что в файлах портов, тестовых целей и ручек media и в сборке живого e2e: переименование не делает
	// страж вакуумным молча (план media Task 11: тестовые цели и ручки /_e2e/media/* — только сборка с тегом)
	tagged := []string{"backend/cmd/api/captaincy_e2e.go", "backend/cmd/admin-api/captaincy_e2e.go",
		"backend/cmd/api/media_e2e.go", "backend/cmd/admin-api/media_e2e.go", "backend/cmd/internal/mediapurposes/e2e.go"}
	// пакет тестовых ручек целиком под тегом: файл без тега попал бы в прод-сборку
	handlers, err := fs.ReadDir(repoFS, "backend/cmd/internal/mediae2e")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range handlers {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			tagged = append(tagged, path.Join("backend/cmd/internal/mediae2e", e.Name()))
		}
	}
	if len(tagged) < 8 {
		t.Fatalf("файлов с тегом %d — пакет тестовых ручек пуст", len(tagged))
	}
	for _, f := range tagged {
		if !strings.HasPrefix(readRepo(t, f), "//go:build "+e2eTag+"\n") {
			t.Errorf("%s: ждали ограничение //go:build %s первой строкой", f, e2eTag)
		}
	}
	sh := readRepo(t, "scripts/e2e-admin.sh")
	// воркер — тоже с тегом: он обрабатывает объекты тестовых целей (R12 плана media — набор целей одинаков у трёх)
	for _, cmd := range []string{"api", "admin-api", "worker"} {
		want := `go build -tags ` + e2eTag + ` -o "../.tools/e2e/` + cmd + `$exe" ./cmd/` + cmd + `)`
		if !strings.Contains(sh, want) {
			t.Errorf("scripts/e2e-admin.sh: нет сборки %s с тегом (%s) — живой e2e не получит тестовые порты", cmd, want)
		}
	}
}

// e2eTests и e2ePackages — тесты под //go:build wf_e2e и их пакеты: без тега они не собираются, поэтому backend:test
// гоняет их отдельной командой (m6 ревью W1 I-4; план media Task 11 — тесты тестовых целей и ручек media).
var (
	e2eTests    = []string{"TestCaptainChecksE2EBuild", "TestMediaE2E", "TestNoPublicURLForPrivate", "TestE2EPurposes"}
	e2ePackages = []string{"./cmd/api", "./cmd/admin-api", "./cmd/worker", "./cmd/internal/mediapurposes"}
)

// Ревью W1 I-4, m6: тесты с тегом wf_e2e без тега не собираются — backend:test (его зовёт backend:ci в
// .github/workflows/backend.yml) гоняет их отдельной командой go test -tags wf_e2e -run '<все e2eTests>' <e2ePackages>;
// lint — go vet с тегом по тем же пакетам и пакету тестовых ручек (golangci-lint файлы с тегом не видит).
func TestE2ECaptainChecksTestInCI(t *testing.T) {
	var tf struct {
		Tasks map[string]struct {
			Cmds []any `yaml:"cmds"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(readRepo(t, "backend/Taskfile.yml")), &tf); err != nil {
		t.Fatalf("backend/Taskfile.yml: %v", err)
	}
	// tagged — в задаче task есть команда cmd с тегом и всеми пакетами pkgs, у которой -run перечисляет все tests
	tagged := func(task, cmd string, pkgs, tests []string) bool {
		for _, c := range tf.Tasks[task].Cmds {
			s, ok := c.(string)
			if !ok || !strings.HasPrefix(s, cmd+" ") || !strings.Contains(s, "-tags "+e2eTag) {
				continue
			}
			f := strings.Fields(s)
			words := map[string]bool{}
			var run []string
			for i, x := range f {
				words[strings.Trim(x, `'"`)] = true
				if x == "-run" && i+1 < len(f) {
					run = strings.Split(strings.Trim(f[i+1], `'"`), "|")
				}
			}
			all := true
			for _, p := range pkgs {
				all = all && words[p]
			}
			for _, name := range tests {
				all = all && slices.Contains(run, name)
			}
			if all {
				return true
			}
		}
		return false
	}
	if !tagged("test", "go test", e2ePackages, e2eTests) {
		t.Errorf("backend/Taskfile.yml: в tasks.test нет go test -tags %s -run '%s' %s", e2eTag,
			strings.Join(e2eTests, "|"), strings.Join(e2ePackages, " "))
	}
	if vet := append(slices.Clone(e2ePackages), "./cmd/internal/mediae2e"); !tagged("lint", "go vet", vet, nil) {
		t.Errorf("backend/Taskfile.yml: в tasks.lint нет go vet -tags %s %s", e2eTag, strings.Join(vet, " "))
	}
	if !strings.Contains(sprintAll(tf.Tasks["ci"].Cmds), "task:test ") {
		t.Error("backend/Taskfile.yml: backend:ci не зовёт test")
	}
	if !strings.Contains(readRepo(t, ".github/workflows/backend.yml"), "./task backend:ci") {
		t.Error(".github/workflows/backend.yml: нет ./task backend:ci — тесты бэкенда в CI не идут")
	}
}

// sprintAll — элементы cmds (строки и {task: …}) одной строкой «key:value».
func sprintAll(cmds []any) string {
	var b strings.Builder
	for _, c := range cmds {
		switch v := c.(type) {
		case string:
			b.WriteString(v + " ")
		case map[string]any:
			for k, x := range v {
				if s, ok := x.(string); ok {
					b.WriteString(k + ":" + s + " ")
				}
			}
		}
	}
	return b.String()
}

// R15, спека media §6.3: финальная стадия прод-образа задаёт признак, по которому config отвергает
// *_BLOB_BACKEND=fs. Строка — после последнего FROM (в стадиях сборки она бы до образа не дошла); имя и значение — те
// же, что читает config.LoadBlob.
func TestDockerfileSetsBuildKind(t *testing.T) {
	want := "ENV " + config.BuildKindVar + "=" + config.BuildKindProd
	lines := strings.Split(readRepo(t, "backend/Dockerfile"), "\n")
	last := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "FROM ") {
			last = i
		}
	}
	if last < 0 {
		t.Fatal("backend/Dockerfile: нет FROM — страж проверяет не тот файл")
	}
	for _, l := range lines[last+1:] {
		if strings.TrimSpace(l) == want {
			return
		}
	}
	t.Errorf("backend/Dockerfile: после последнего FROM нет строки %q — прод-образ примет *_BLOB_BACKEND=fs", want)
}
