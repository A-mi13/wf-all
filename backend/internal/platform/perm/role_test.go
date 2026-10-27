package perm_test

import (
	"testing"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

// auth.RoleModerator — копия perm.Moderator строкой (perm импортирует auth, обратно нельзя). Разойдутся —
// Staff.InScope сочтёт модератора глобальной ролью и откроет ему все города.
func TestRoleModeratorMatchesAuth(t *testing.T) {
	if string(perm.Moderator) != auth.RoleModerator {
		t.Fatalf("perm.Moderator = %q, auth.RoleModerator = %q — строки роли разошлись", perm.Moderator, auth.RoleModerator)
	}
}
