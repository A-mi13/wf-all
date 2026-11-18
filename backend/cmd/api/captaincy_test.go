//go:build !wf_e2e

package main

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
)

// Прод-сборка (без тега wf_e2e) подключает для обоих портов условий капитанства только «недоступно» (спека identity
// §10.3, К1, R4 плана I-4): ровно RequiredCaptainChecks, каждый — checks_unavailable; пакет identitytest в
// зависимостях бинарника отсутствует.
func TestCaptainChecksProdBuild(t *testing.T) {
	t.Parallel()
	cs := captainChecks()
	if err := identity.ValidateCaptainChecks(cs); err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		reason, ok, err := c.Check(context.Background(), nil, uuid.New(), uuid.New())
		if err != nil || ok || reason != "checks_unavailable" {
			t.Errorf("%s: %q %v %v — прод-сборка обходит правило капитанства", c.Name(), reason, ok, err)
		}
	}
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", ".")
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("go list: %v\n%s", err, ee.Stderr)
		}
		t.Fatal(err)
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, "wf/backend/internal/identity") {
		t.Fatal("go list не вернул identity — проверка вхолостую")
	}
	if slices.Contains(deps, "wf/backend/internal/identity/identitytest") {
		t.Fatal("прод-бинарник тянет identity/identitytest")
	}
}
