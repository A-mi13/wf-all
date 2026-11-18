package identitytest_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/identitytest"
)

// Тестовые порты: Allow — ok, Deny — заданная причина, AllowAll — ровно набор RequiredCaptainChecks; базы не трогают.
func TestChecks(t *testing.T) {
	t.Parallel()
	ctx, u, c := context.Background(), uuid.New(), uuid.New()
	if r, ok, err := identitytest.AllowCheck("players.profile_complete").Check(ctx, nil, u, c); !ok || r != "" || err != nil {
		t.Fatalf("Allow: %q %v %v", r, ok, err)
	}
	d := identitytest.DenyCheck("players.profile_complete", "profile_incomplete")
	if r, ok, err := d.Check(ctx, nil, u, c); ok || r != "profile_incomplete" || err != nil || d.Name() != "players.profile_complete" {
		t.Fatalf("Deny: %q %v %v %q", r, ok, err, d.Name())
	}
	all := identitytest.AllowAll()
	if err := identity.ValidateCaptainChecks(all); err != nil {
		t.Fatal(err)
	}
	for _, ch := range all {
		if _, ok, err := ch.Check(ctx, nil, u, c); !ok || err != nil {
			t.Fatalf("%s: %v %v", ch.Name(), ok, err)
		}
	}
}
