package config_test

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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
	// имя тега то же, что в файлах портов и в сборке живого e2e: переименование не делает страж вакуумным молча
	for _, f := range []string{"backend/cmd/api/captaincy_e2e.go", "backend/cmd/admin-api/captaincy_e2e.go"} {
		if !strings.HasPrefix(readRepo(t, f), "//go:build "+e2eTag+"\n") {
			t.Errorf("%s: ждали ограничение //go:build %s первой строкой", f, e2eTag)
		}
	}
	sh := readRepo(t, "scripts/e2e-admin.sh")
	for _, cmd := range []string{"api", "admin-api"} {
		want := `go build -tags ` + e2eTag + ` -o "../.tools/e2e/` + cmd + `$exe" ./cmd/` + cmd + `)`
		if !strings.Contains(sh, want) {
			t.Errorf("scripts/e2e-admin.sh: нет сборки %s с тегом (%s) — живой e2e не получит тестовые порты", cmd, want)
		}
	}
}

// Ревью W1 I-4, m6: тест тестовых портов (TestCaptainChecksE2EBuild, файл под //go:build wf_e2e) без тега не
// собирается — backend:test (его зовёт backend:ci в .github/workflows/backend.yml) гоняет его отдельной командой.
func TestE2ECaptainChecksTestInCI(t *testing.T) {
	var tf struct {
		Tasks map[string]struct {
			Cmds []any `yaml:"cmds"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(readRepo(t, "backend/Taskfile.yml")), &tf); err != nil {
		t.Fatalf("backend/Taskfile.yml: %v", err)
	}
	found := false
	for _, c := range tf.Tasks["test"].Cmds {
		s, ok := c.(string)
		if !ok {
			continue
		}
		f := strings.Fields(s)
		has := func(w string) bool {
			for _, x := range f {
				if strings.Trim(x, `'"`) == w {
					return true
				}
			}
			return false
		}
		if strings.HasPrefix(s, "go test ") && strings.Contains(s, "-tags "+e2eTag) &&
			has("./cmd/api") && has("./cmd/admin-api") && has("TestCaptainChecksE2EBuild") {
			found = true
		}
	}
	if !found {
		t.Errorf("backend/Taskfile.yml: в tasks.test нет go test -tags %s … TestCaptainChecksE2EBuild ./cmd/api ./cmd/admin-api", e2eTag)
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
