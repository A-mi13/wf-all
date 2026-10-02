package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
)

func TestPrincipalRolesAndRestrictions(t *testing.T) {
	moscow := uuid.New()
	p := &auth.Principal{
		Roles: []auth.Role{
			{Name: "admin", ScopeType: "global"},
			{Name: "captain", ScopeType: "city", ScopeID: moscow},
		},
		Restrictions: []string{"comment"},
	}
	if !p.HasRole("admin", "global", uuid.Nil) || !p.HasRole("captain", "city", moscow) {
		t.Fatal("выданные роли не видны")
	}
	// скоуп точный: капитан в Москве — не капитан в другом городе; глобальная роль не
	// подразумевает городскую
	if p.HasRole("captain", "city", uuid.New()) || p.HasRole("city_moderator", "city", moscow) {
		t.Fatal("роль вне скоупа")
	}
	if !p.Restricted("comment") || p.Restricted("create_match") {
		t.Fatal("ограничения")
	}
}

func TestContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := auth.From(ctx); ok || auth.Authenticated(ctx) {
		t.Fatal("Principal в пустом ctx")
	}
	p := &auth.Principal{UserID: uuid.New()}
	ctx = auth.With(ctx, p)
	if got, ok := auth.From(ctx); !ok || got != p || !auth.Authenticated(ctx) {
		t.Fatal("Principal не найден")
	}
}
