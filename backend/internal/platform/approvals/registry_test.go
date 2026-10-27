package approvals_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
)

func noop(context.Context, pgx.Tx, json.RawMessage, *auth.Staff) error { return nil }

func noCheck(context.Context, pgx.Tx, approvals.Approval) error { return nil }

// mustPanic — f паникует с сообщением, содержащим want.
func mustPanic(t *testing.T, name, want string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		p := recover()
		if p == nil {
			t.Fatalf("%s: паники нет", name)
		}
		if msg, _ := p.(string); !strings.Contains(msg, want) {
			t.Fatalf("%s: паника %v, ждали %q", name, p, want)
		}
	}()
	f()
}

// Ошибки сборки реестра — паника: имя не по формату CHECK approvals_action_format, нет
// исполнителя, право не из каталога или черновое, дубль имени, вторая проверка инициатора.
func TestRegistryPanics(t *testing.T) {
	for _, name := range []string{"", "staff", "Staff.invite", "staff.invite.x", "staff.", ".invite", "staff.in-vite", "1staff.invite"} {
		mustPanic(t, "имя "+name, "не по формату", func() {
			approvals.NewRegistry().Register(approvals.Action{Name: name, Permission: "staff.roles.grant", Exec: noop})
		})
	}
	mustPanic(t, "без исполнителя", "без исполнителя", func() {
		approvals.NewRegistry().Register(approvals.Action{Name: "staff.invite", Permission: "staff.roles.grant"})
	})
	mustPanic(t, "неизвестное право", "не из каталога", func() {
		approvals.NewRegistry().Register(approvals.Action{Name: "staff.invite", Permission: "staff.roles.nope", Exec: noop})
	})
	mustPanic(t, "черновое право", "черновое", func() {
		approvals.NewRegistry().Register(approvals.Action{Name: "teams.approve", Permission: "teams.captains.approve", Exec: noop})
	})
	r := approvals.NewRegistry()
	r.Register(approvals.Action{Name: "staff.invite", Permission: "staff.roles.grant", Exec: noop})
	mustPanic(t, "дубль", "уже зарегистрировано", func() {
		r.Register(approvals.Action{Name: "staff.invite", Permission: "staff.access.reset", Exec: noop})
	})
	r.SetInitiatorCheck(noCheck)
	mustPanic(t, "вторая проверка", "уже зарегистрирована", func() { r.SetInitiatorCheck(noCheck) })
	mustPanic(t, "пустая проверка", "пустая", func() { approvals.NewRegistry().SetInitiatorCheck(nil) })
}

// Actions — имена по алфавиту (страж подписей approvals.actions.<действие>, D21) и копия.
func TestRegistryActionsSorted(t *testing.T) {
	r := approvals.NewRegistry()
	for _, n := range []string{"staff.role_change", "staff.access_reset", "staff.revoke_owner", "staff.invite"} {
		r.Register(approvals.Action{Name: n, Permission: "staff.roles.grant", Exec: noop})
	}
	got := r.Actions()
	want := []string{"staff.access_reset", "staff.invite", "staff.revoke_owner", "staff.role_change"}
	if !slices.Equal(got, want) {
		t.Fatalf("Actions = %v, ждали %v", got, want)
	}
	got[0] = "x.y"
	if r.Actions()[0] != "staff.access_reset" {
		t.Fatal("Actions отдаёт внутренний срез")
	}
}

// Validate — действия без проверки инициатора не готовы: сборка 10c зовёт до старта сервера.
func TestRegistryValidate(t *testing.T) {
	r := approvals.NewRegistry()
	if err := r.Validate(); err != nil {
		t.Fatalf("пустой реестр: %v", err)
	}
	r.Register(approvals.Action{Name: "staff.invite", Permission: "staff.roles.grant", Exec: noop})
	if err := r.Validate(); err == nil {
		t.Fatal("действие без проверки инициатора прошло Validate")
	}
	r.SetInitiatorCheck(noCheck)
	if err := r.Validate(); err != nil {
		t.Fatalf("готовый реестр: %v", err)
	}
}
