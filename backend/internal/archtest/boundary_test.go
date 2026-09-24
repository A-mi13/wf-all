// Package archtest — архитектурные стражи, которые не выразить линтером.
package archtest

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Бинарник публичного API не должен даже транзитивно тянуть код админки.
// depguard видит только прямые импорты — этот тест ловит и транзитивные.
func TestPublicAPIDoesNotDependOnAdmin(t *testing.T) {
	forbidden := []string{"wf/backend/internal/httpapi/admin", "wf/backend/cmd/admin-api"}
	for _, goos := range []string{"linux", "windows"} {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./cmd/api")
		cmd.Dir = "../.." // корень модуля backend
		cmd.Env = append(cmd.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				t.Fatalf("go list (%s): %v\n%s", goos, err, ee.Stderr)
			}
			t.Fatal(err)
		}
		for _, pkg := range strings.Fields(string(out)) {
			for _, f := range forbidden {
				if pkg == f || strings.HasPrefix(pkg, f+"/") {
					t.Errorf("GOOS=%s: cmd/api зависит от %s", goos, pkg)
				}
			}
		}
	}
}
