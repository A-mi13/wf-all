// Package archtest — архитектурные стражи, которые не выразить линтером.
package archtest

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Бинарник публичного API не должен даже транзитивно тянуть код админки.
// depguard видит только прямые импорты — этот тест ловит и транзитивные.
//
// required — заведомо нужные зависимости cmd/api. Если `go list -deps` вернёт
// пустой или тривиальный граф (например, в cmd/api ещё нет реального кода —
// так было на этапе RED в задаче 10), проверка forbidden-пакетов пройдёт
// вхолостую за счёт пустого набора, ничего не поймав. required страхует
// именно от этого: без него сам страж не проверяем.
func TestPublicAPIDoesNotDependOnAdmin(t *testing.T) {
	forbidden := []string{"wf/backend/internal/httpapi/admin", "wf/backend/cmd/admin-api"}
	required := []string{"wf/backend/internal/platform/config", "wf/backend/internal/httpapi/public"}
	for _, goos := range []string{"linux", "windows"} {
		cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-f", "{{.ImportPath}}", "./cmd/api")
		cmd.Dir = "../.." // корень модуля backend
		cmd.Env = append(cmd.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := errors.AsType[*exec.ExitError](err); ok {
				t.Fatalf("go list (%s): %v\n%s", goos, err, ee.Stderr)
			}
			t.Fatal(err)
		}
		deps := make(map[string]bool)
		for pkg := range strings.FieldsSeq(string(out)) {
			deps[pkg] = true
		}
		for _, req := range required {
			if !deps[req] {
				t.Fatalf("GOOS=%s: go list -deps ./cmd/api не содержит ожидаемый пакет %s — граф зависимостей пуст или тривиален, страж проверяет вхолостую", goos, req)
			}
		}
		for pkg := range deps {
			for _, f := range forbidden {
				if pkg == f || strings.HasPrefix(pkg, f+"/") {
					t.Errorf("GOOS=%s: cmd/api зависит от %s", goos, pkg)
				}
			}
		}
	}
}
