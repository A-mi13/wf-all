// Package archtest — архитектурные стражи, которые не выразить линтером.
package archtest

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Публичные бинарники (api, worker) не должны даже транзитивно тянуть код
// админки. Админский код — любой пакет модуля wf/backend, у которого один из
// сегментов пути равен "admin" (internal/httpapi/admin, internal/<модуль>/admin
// и т.п.), плюс сам cmd/admin-api. depguard видит только прямые импорты и
// сравнивает пакеты по префиксу — этот тест ловит и транзитивные, и любой
// сегмент admin; он — главный страж границы.
//
// required — заведомо нужные зависимости бинарника. Если `go list -deps`
// вернёт пустой или тривиальный граф (например, в cmd/api ещё нет реального
// кода — так было на этапе RED в задаче 10), проверка пройдёт вхолостую за
// счёт пустого набора, ничего не поймав. required страхует именно от этого.
func TestPublicBinariesDoNotDependOnAdmin(t *testing.T) {
	binaries := []struct {
		pkg      string
		required []string
	}{
		{"./cmd/api", []string{"wf/backend/internal/platform/config", "wf/backend/internal/httpapi/public"}},
		{"./cmd/worker", []string{"wf/backend/internal/platform/config", "wf/backend/internal/platform/queue"}},
	}
	for _, bin := range binaries {
		for _, goos := range []string{"linux", "windows"} {
			deps := listDeps(t, bin.pkg, goos)
			for _, req := range bin.required {
				if !deps[req] {
					t.Fatalf("GOOS=%s: go list -deps %s не содержит ожидаемый пакет %s — граф зависимостей пуст или тривиален, страж проверяет вхолостую", goos, bin.pkg, req)
				}
			}
			for pkg := range deps {
				if isAdminPackage(pkg) {
					t.Errorf("GOOS=%s: %s зависит от админского пакета %s", goos, bin.pkg, pkg)
				}
			}
		}
	}
}

// Сам предикат проверяем отдельно: и что ловит, и что не ловит лишнего.
func TestIsAdminPackage(t *testing.T) {
	cases := map[string]bool{
		"wf/backend/internal/httpapi/admin":     true,
		"wf/backend/internal/httpapi/admin/gen": true,
		"wf/backend/internal/matches/admin":     true,
		"wf/backend/cmd/admin-api":              true,
		"wf/backend/admin":                      true,
		"wf/backend/internal/httpapi/public":    false,
		"wf/backend/internal/adminx":            false,
		"wf/backend/internal/superadmin":        false,
		"wf/backend/cmd/api":                    false,
		"github.com/example/admin":              false, // чужой модуль — не наш админский код
	}
	for pkg, want := range cases {
		if got := isAdminPackage(pkg); got != want {
			t.Errorf("isAdminPackage(%q) = %v, want %v", pkg, got, want)
		}
	}
}

func isAdminPackage(pkg string) bool {
	rest, ok := strings.CutPrefix(pkg, "wf/backend/")
	if !ok {
		return false
	}
	if rest == "cmd/admin-api" || strings.HasPrefix(rest, "cmd/admin-api/") {
		return true
	}
	return slices.Contains(strings.Split(rest, "/"), "admin")
}

func listDeps(t *testing.T, pkg, goos string) map[string]bool {
	t.Helper()
	// pkg — из таблицы теста, не внешний ввод.
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-f", "{{.ImportPath}}", pkg) //nolint:gosec // см. выше
	// корень модуля backend
	cmd.Dir = "../.."
	cmd.Env = append(cmd.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("go list %s (%s): %v\n%s", pkg, goos, err, ee.Stderr)
		}
		t.Fatal(err)
	}
	deps := make(map[string]bool)
	for p := range strings.FieldsSeq(string(out)) {
		deps[p] = true
	}
	return deps
}
