package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	identityadmin "wf/backend/internal/identity/admin"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/clock"
)

// Сборка identity в admin-api (план I-2, Task 17): действие identity.mfa_reset — в том же реестре, что staff и geo,
// до Validate; хендлер identity собран (Options.Identity) с клиентом вставки staff (письма — очередь mail).
func TestAssembleRegistersIdentityActions(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	a := e.assemble()
	if acts := a.reg.Actions(); !slices.Contains(acts, "identity.mfa_reset") {
		t.Fatalf("в реестре нет identity.mfa_reset: %v", acts)
	}
	if a.identity == nil {
		t.Fatal("хендлер identity не собран")
	}
	// identity.mfa_reset — LockActors: без хука замка акторов staff реестр не проходит Validate (fail-closed)
	reg := approvals.NewRegistry()
	ih, err := identityadmin.New(identity.New(a.deps.Pool), identityadmin.Deps{Pool: a.deps.Pool, Clock: clock.System,
		River: a.deps.River, Log: a.deps.Log})
	if err != nil {
		t.Fatal(err)
	}
	ih.RegisterApprovals(reg, approvals.New(a.deps.Pool, clock.System, reg))
	reg.SetInitiatorCheck(func(context.Context, pgx.Tx, approvals.Approval) error { return nil })
	if err := reg.Validate(); err == nil || !strings.Contains(err.Error(), "identity.mfa_reset") {
		t.Fatalf("сборка без хука staff: Validate = %v, ждали отказ", err)
	}
}
