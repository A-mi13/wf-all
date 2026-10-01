package archtest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const p = "wf/backend/internal/"

// Подсадка багов: каждое нарушение из спеки §4.3–4.4 должно ловиться.
func TestImportViolation(t *testing.T) {
	allowed := [][2]string{
		{p + "teams/internal/app", p + "identity"},
		{p + "teams/internal/app", p + "identity/intx"},
		{p + "results/internal/app", p + "matches/intx"},
		{p + "moderation/internal/app", p + "identity/intx"},
		{p + "matches/internal/store", p + "platform/db"},
		{p + "teams/httpapi", p + "httpapi/public/oapi"},
		{p + "teams/admin", p + "httpapi/admin/oapi"},
		{p + "httpapi/public", p + "teams/httpapi"},
		{p + "platform/httpx", p + "platform/logx"},
		{"wf/backend/cmd/api", p + "teams/internal/app"},
		{p + "teams", "github.com/jackc/pgx/v5"},
		{p + "stats/subscribers", p + "results"},
		{p + "httpapi/public", p + "teams"},                    // сборка сервера — корневой пакет модуля
		{p + "httpapi/admin", p + "teams/admin"},               // и его admin/
		{p + "httpapi/public", p + "httpapi/public/oapi"},      // внутри httpapi
		{p + "platform/testkit/apitest", p + "platform/httpx"}, // внутри платформы
	}
	for _, c := range allowed {
		if v := importViolation(c[0], c[1]); v != "" {
			t.Errorf("%s → %s должен быть разрешён, а: %s", c[0], c[1], v)
		}
	}
	forbidden := [][2]string{
		{p + "geo", p + "identity"},                             // вверх по слоям
		{p + "teams/internal/app", p + "matches"},               // вверх по слоям
		{p + "stats", p + "notify"},                             // верхний слой друг друга не импортирует
		{p + "teams/internal/app", p + "identity/internal/app"}, // не корневой пакет
		{p + "teams/internal/app", p + "identity/httpapi"},      // не корневой пакет
		{p + "matches/internal/app", p + "identity/intx"},       // пары нет в §4.4
		{p + "platform/db", p + "teams"},                        // платформа не зависит от модулей
		{p + "teams/internal/app", p + "httpapi/public/oapi"},   // oapi — только из своего httpapi/
		{p + "teams/admin", p + "httpapi/public/oapi"},          // admin/ — только admin/oapi
		{p + "teams/httpapi", p + "httpapi/public"},             // сборка сервера
		{p + "teams", p + "archtest"},
		{p + "httpapi/public", p + "identity/intx"},           // intx — только из пар §4.4
		{p + "httpapi/public", p + "teams/jobs"},              // не корневой пакет и не httpapi/admin
		{p + "httpapi/public", p + "teams/subscribers"},       // то же
		{p + "httpapi/admin", p + "teams/internal/app"},       // то же
		{p + "archtest", p + "matches/internal/store"},        // то же — для любого немодульного пакета
		{p + "platform/db", p + "httpapi/public/oapi"},        // платформа не зависит от сборки сервера
		{p + "platform/testkit/apitest", p + "httpapi/admin"}, // то же
		{p + "platform/db", p + "archtest"},                   // платформа не зависит от стражей
		{p + "platform/db", p + "archtest/sqlscan"},           // то же
	}
	for _, c := range forbidden {
		if v := importViolation(c[0], c[1]); v == "" {
			t.Errorf("%s → %s должен быть запрещён", c[0], c[1])
		}
	}
}

// Список пар §4.4 согласован с проверкой импортов: каждая пара — разрешённый импорт.
func TestIntxPairsAreAllowedImports(t *testing.T) {
	for pair := range IntxPairs {
		from, to := p+pair[0]+"/internal/app", p+pair[1]+"/intx"
		if v := importViolation(from, to); v != "" {
			t.Errorf("пара %v из IntxPairs запрещена проверкой импортов: %s", pair, v)
		}
	}
}

func TestInternalDirsAreKnown(t *testing.T) {
	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !isKnownOwner(e.Name()) && !infra[e.Name()] {
			t.Errorf("internal/%s: неизвестный модуль — добавь его в archtest.Layers (спека §4.3)", e.Name())
		}
	}
}

// Главный страж: прямые импорты всех пакетов модуля wf/backend, включая тесты.
func TestModuleImportsRespectBoundaries(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-f",
		`{{.ImportPath}}{{range .Imports}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}`,
		"./...")
	cmd.Dir = "../.." // корень модуля backend
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("go list: %v\n%s", err, ee.Stderr)
		}
		t.Fatal(err)
	}
	seen := false
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		from := fields[0]
		if from == p+"platform/httpx" {
			seen = true
		}
		for _, to := range fields[1:] {
			if v := importViolation(from, to); v != "" {
				t.Errorf("%s → %s: %s", from, to, v)
			}
		}
	}
	if !seen {
		t.Fatal("go list не вернул internal/platform/httpx — страж проверяет вхолостую")
	}
}
