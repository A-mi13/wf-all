package auth_test

import (
	"testing"

	"wf/backend/internal/platform/auth"
)

// Значения прав уходят наружу строками (docs/05, permissions.json, GET /v1/staff/me); нулевое и
// незнакомое значение — отказ: забытая строка матрицы не даёт доступа (D20: значения read нет).
func TestGrantString(t *testing.T) {
	var zero auth.Grant
	if zero != auth.GrantDeny {
		t.Fatal("нулевое значение Grant — не GrantDeny")
	}
	for g, want := range map[auth.Grant]string{
		auth.GrantDeny:   "deny",
		auth.GrantAllow:  "allow",
		auth.GrantScoped: "scoped",
		auth.GrantDual:   "dual",
		auth.Grant(42):   "deny",
	} {
		if got := g.String(); got != want {
			t.Errorf("Grant(%d).String() = %q, нужно %q", uint8(g), got, want)
		}
	}
}
