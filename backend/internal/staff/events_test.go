package staff_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/staff"
)

var (
	staffID = uuid.MustParse("0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d")
	cityA   = uuid.MustParse("3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47")
	cityB   = uuid.MustParse("7a1d5e9c-2b4f-4c8a-b3e6-1f0d9c8b7a65")
)

func payload(t *testing.T, e events.Event) string {
	t.Helper()
	b, err := json.Marshal(e.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// События staff — агрегат учётка, version — staff_accounts.version после изменения; payload — только
// id и роль (без почты и имени). staff.role_revoked: выключение — с ролью, снятие только городов
// модератора (решение контроллера 06.10, п. 7) — со снятыми городами; пустые поля не уходят.
func TestEventPayloads(t *testing.T) {
	until := time.Date(2026, 10, 13, 13, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		e    events.Event
		typ  string
		json string
	}{
		{"invited", staff.InvitedEvent(staffID, 1, "admin"), staff.EventInvited,
			`{"staff_id":"` + staffID.String() + `","role":"admin"}`},
		{"role_granted", staff.RoleGrantedEvent(staffID, 3, "moderator", []uuid.UUID{cityA, cityB}), staff.EventRoleGranted,
			`{"staff_id":"` + staffID.String() + `","role":"moderator","city_ids":["` + cityA.String() + `","` + cityB.String() + `"]}`},
		{"role_granted_global", staff.RoleGrantedEvent(staffID, 3, "admin", nil), staff.EventRoleGranted,
			`{"staff_id":"` + staffID.String() + `","role":"admin"}`},
		{"role_revoked", staff.RoleRevokedEvent(staffID, 4, "admin"), staff.EventRoleRevoked,
			`{"staff_id":"` + staffID.String() + `","role":"admin"}`},
		{"cities_revoked", staff.CitiesRevokedEvent(staffID, 5, []uuid.UUID{cityB}), staff.EventRoleRevoked,
			`{"staff_id":"` + staffID.String() + `","removed_city_ids":["` + cityB.String() + `"]}`},
		{"access_reset", staff.AccessResetEvent(staffID, 6), staff.EventAccessReset,
			`{"staff_id":"` + staffID.String() + `"}`},
		{"locked", staff.LockedEvent(staffID, 7, until), staff.EventLocked,
			`{"staff_id":"` + staffID.String() + `","locked_until":"2026-10-13T13:00:00Z"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.e.Type != c.typ || c.e.SchemaVersion != 1 || c.e.AggregateType != staff.AggregateAccount ||
				c.e.AggregateID != staffID || c.e.AggregateVersion < 1 {
				t.Fatalf("конверт %+v", c.e)
			}
			if got := payload(t, c.e); got != c.json {
				t.Fatalf("payload %s, ждали %s", got, c.json)
			}
		})
	}
	if v := staff.CitiesRevokedEvent(staffID, 5, nil).AggregateVersion; v != 5 {
		t.Fatalf("версия %d", v)
	}
}

// Имена событий — <модуль>.<имя> (§4.12).
func TestEventNames(t *testing.T) {
	want := map[string]string{
		staff.EventInvited: "staff.invited", staff.EventRoleGranted: "staff.role_granted",
		staff.EventRoleRevoked: "staff.role_revoked", staff.EventAccessReset: "staff.access_reset",
		staff.EventLocked: "staff.locked",
	}
	for got, w := range want {
		if got != w {
			t.Errorf("событие %q, ждали %q", got, w)
		}
	}
	if staff.AggregateAccount != "staff_account" {
		t.Fatalf("агрегат %q", staff.AggregateAccount)
	}
}
