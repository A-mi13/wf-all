//go:build wf_e2e

package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
)

// Сборка с тегом wf_e2e (живой e2e) подключает тестовые порты: оба условия выполнены (спека identity §10.3).
func TestCaptainChecksE2EBuild(t *testing.T) {
	t.Parallel()
	cs := captainChecks()
	if err := identity.ValidateCaptainChecks(cs); err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if reason, ok, err := c.Check(context.Background(), nil, uuid.New(), uuid.New()); !ok || err != nil {
			t.Errorf("%s: %q %v %v", c.Name(), reason, ok, err)
		}
	}
}
