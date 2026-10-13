package auth_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
)

// Ключи прав теста — не из каталога perm: платформа проверяет механику Grant, а не матрицу.
var testGrants = map[string]auth.Grant{
	"test.allow":  auth.GrantAllow,
	"test.scoped": auth.GrantScoped,
	"test.dual":   auth.GrantDual,
	"test.deny":   auth.GrantDeny,
}

func activeStaff(role string, cities ...uuid.UUID) *auth.Staff {
	return &auth.Staff{StaffID: uuid.New(), SessionID: uuid.New(), Role: role, CityIDs: cities,
		Stage: auth.StageActive, Grants: testGrants}
}

func TestStaffGrantUnknownIsDeny(t *testing.T) {
	s := activeStaff("admin")
	if s.Grant("test.allow") != auth.GrantAllow || s.Grant("test.dual") != auth.GrantDual {
		t.Fatal("выданные права не видны")
	}
	if g := s.Grant("test.missing"); g != auth.GrantDeny {
		t.Fatalf("нет ключа: %v, нужен GrantDeny", g)
	}
	if g := (&auth.Staff{}).Grant("test.allow"); g != auth.GrantDeny {
		t.Fatalf("nil-карта прав: %v", g)
	}
}

func TestStaffInScope(t *testing.T) {
	moscow, spb := uuid.New(), uuid.New()
	mod := activeStaff(auth.RoleModerator, moscow)
	if !mod.InScope(moscow) || mod.InScope(spb) || mod.InScope(uuid.Nil) {
		t.Fatal("скоуп модератора: только его города")
	}
	// модератор без городов (сломанная запись) не видит ничего, а не всё
	if activeStaff(auth.RoleModerator).InScope(moscow) {
		t.Fatal("модератор без городов в скоупе")
	}
	for _, role := range []string{"owner", "admin", "support"} {
		if s := activeStaff(role); !s.InScope(moscow) || !s.InScope(spb) {
			t.Fatalf("%s: глобальная роль не видит город", role)
		}
	}
}

func TestStaffDecideAndCan(t *testing.T) {
	moscow, spb := uuid.New(), uuid.New()
	admin, mod := activeStaff("admin"), activeStaff(auth.RoleModerator, moscow)
	cases := []struct {
		name string
		s    *auth.Staff
		perm string
		city uuid.UUID
		want auth.Decision
	}{
		{"allow без города", admin, "test.allow", uuid.Nil, auth.Allow},
		{"allow с городом", mod, "test.allow", spb, auth.Allow},
		{"dual — через подтверждение", admin, "test.dual", uuid.Nil, auth.NeedsApproval},
		{"dual с городом", mod, "test.dual", moscow, auth.NeedsApproval},
		{"deny", admin, "test.deny", moscow, auth.Deny},
		{"нет права", admin, "test.missing", moscow, auth.Deny},
		{"scoped глобальной роли", admin, "test.scoped", spb, auth.Allow},
		{"scoped без города — нет даже у глобальной", admin, "test.scoped", uuid.Nil, auth.Deny},
		{"scoped модератора, свой город", mod, "test.scoped", moscow, auth.Allow},
		{"scoped модератора, чужой город", mod, "test.scoped", spb, auth.Deny},
		{"scoped модератора без города", mod, "test.scoped", uuid.Nil, auth.Deny},
	}
	for _, c := range cases {
		if got := c.s.Decide(c.perm, c.city); got != c.want {
			t.Errorf("%s: Decide = %v, нужно %v", c.name, got, c.want)
		}
		// Can — «исполнить сразу»: только Allow; Dual сразу не исполняется (P6)
		if got := c.s.Can(c.perm, c.city); got != (c.want == auth.Allow) {
			t.Errorf("%s: Can = %v", c.name, got)
		}
	}
}

// Права каталога — только в активной сессии без Reauth: модуль, решающий из сценария, не
// разрешит действие недовошедшему сотруднику, даже если конвейер ошибся.
func TestStaffDecideNeedsActiveSession(t *testing.T) {
	for name, mutate := range map[string]func(*auth.Staff){
		"mfa_pending": func(s *auth.Staff) { s.Stage = auth.StageMFAPending },
		"setup":       func(s *auth.Staff) { s.Stage = auth.StageSetup },
		"без стадии":  func(s *auth.Staff) { s.Stage = "" },
		"reauth":      func(s *auth.Staff) { s.Reauth = true },
	} {
		s := activeStaff("owner")
		mutate(s)
		for _, perm := range []string{"test.allow", "test.dual", "test.scoped"} {
			if d := s.Decide(perm, uuid.New()); d != auth.Deny {
				t.Errorf("%s: %s → %v, нужен Deny", name, perm, d)
			}
		}
		// Grant не смотрит на стадию: по нему конвейер отличает 403 от 401
		if s.Grant("test.allow") != auth.GrantAllow {
			t.Errorf("%s: Grant зависит от стадии", name)
		}
	}
}

func TestStaffContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := auth.StaffFrom(ctx); ok || auth.AuthenticatedStaff(ctx) {
		t.Fatal("сотрудник в пустом ctx")
	}
	if _, ok := auth.StaffFrom(auth.WithStaff(ctx, nil)); ok {
		t.Fatal("nil-сотрудник принят за вошедшего")
	}
	s := activeStaff("admin")
	ctx = auth.WithStaff(ctx, s)
	if got, ok := auth.StaffFrom(ctx); !ok || got != s || !auth.AuthenticatedStaff(ctx) {
		t.Fatal("сотрудник не найден")
	}
	// сотрудник — не игрок: Principal публичного API из ctx сотрудника не достаётся
	if _, ok := auth.From(ctx); ok || auth.Authenticated(ctx) {
		t.Fatal("сотрудник виден как Principal")
	}
}

func TestStaffSessionsFunc(t *testing.T) {
	ip := netip.MustParseAddr("203.0.113.7")
	want := activeStaff("admin")
	var background bool
	var l auth.StaffSessions = auth.StaffSessionsFunc(func(_ context.Context, token string, got netip.Addr, bg bool) (*auth.Staff, error) {
		background = bg
		if token != "tok" || got != ip {
			return nil, auth.ErrSessionInvalid
		}
		return want, nil
	})
	if s, err := l.LoadStaff(context.Background(), "tok", ip, true); err != nil || s != want || !background {
		t.Fatalf("LoadStaff: %v %v, фоновый %v", s, err, background)
	}
	if _, err := l.LoadStaff(context.Background(), "other", ip, false); !errors.Is(err, auth.ErrSessionInvalid) || background {
		t.Fatalf("чужой токен: %v, фоновый %v", err, background)
	}
}

// NoStaffSessions — до модуля staff (Task 6a/10c): сессий сотрудников нет, любой токен —
// ErrSessionInvalid (конвейер admin-api стирает cookie и считает отказ в IP-лимите).
func TestNoStaffSessions(t *testing.T) {
	s, err := auth.NoStaffSessions.LoadStaff(context.Background(), "tok", netip.MustParseAddr("203.0.113.7"), false)
	if s != nil || !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("NoStaffSessions: %v, %v", s, err)
	}
}
