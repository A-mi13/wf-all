package app_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

// Таблица D16 целиком: право по изменению (старая или новая роль owner/admin — grant_elevated; роль
// меняется или добавлен город — grant; только снятие городов — revoke, сразу), Allow — 200 (nil),
// NeedsApproval — 202 (запрос с правом), Deny — 403, без изменений — nil без записей.
func TestChangeRoleD16(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c1, c2, c3 := e.city("kazan"), e.city("perm"), e.city("ufa")
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	admin := e.as(e.addMember("admin@wf.example", memberOpts{Role: "admin"}))
	modActor := e.as(e.addMember("modactor@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c1}}))
	n := 0
	target := func(role string, cities ...uuid.UUID) member {
		n++
		return e.addMember("t"+strconv.Itoa(n)+"@wf.example", memberOpts{Role: role, Cities: cities})
	}
	const (
		now  = "now"  // исполнено сразу
		ask  = "ask"  // запрос
		deny = "deny" // 403
		same = "same" // без изменений
	)
	cases := []struct {
		name     string
		by       *auth.Staff
		from     string
		fromC    []uuid.UUID
		to       perm.Role
		toC      []uuid.UUID
		want     string
		wantPerm string
	}{
		{"владелец: снять город", owner, "moderator", []uuid.UUID{c1, c2}, perm.Moderator, []uuid.UUID{c1}, now, ""},
		{"админ: снять город", admin, "moderator", []uuid.UUID{c1, c2}, perm.Moderator, []uuid.UUID{c2}, now, ""},
		{"владелец: добавить город", owner, "moderator", []uuid.UUID{c1}, perm.Moderator, []uuid.UUID{c1, c2}, ask, "staff.roles.grant"},
		{"админ: добавить город", admin, "moderator", []uuid.UUID{c1}, perm.Moderator, []uuid.UUID{c1, c2}, ask, "staff.roles.grant"},
		{"админ: заменить город", admin, "moderator", []uuid.UUID{c1, c2}, perm.Moderator, []uuid.UUID{c1, c3}, ask, "staff.roles.grant"},
		{"админ: модератор → поддержка", admin, "moderator", []uuid.UUID{c1}, perm.Support, nil, ask, "staff.roles.grant"},
		{"админ: поддержка → модератор", admin, "support", nil, perm.Moderator, []uuid.UUID{c1}, ask, "staff.roles.grant"},
		{"владелец: поддержка → админ", owner, "support", nil, perm.Admin, nil, ask, "staff.roles.grant_elevated"},
		{"владелец: админ → владелец", owner, "admin", nil, perm.Owner, nil, ask, "staff.roles.grant_elevated"},
		{"владелец: владелец → админ", owner, "owner", nil, perm.Admin, nil, ask, "staff.roles.grant_elevated"},
		{"владелец: админ → поддержка", owner, "admin", nil, perm.Support, nil, ask, "staff.roles.grant_elevated"},
		{"админ: поддержка → админ", admin, "support", nil, perm.Admin, nil, deny, ""},
		{"админ: админ → поддержка", admin, "admin", nil, perm.Support, nil, deny, ""},
		{"админ: владелец → поддержка", admin, "owner", nil, perm.Support, nil, deny, ""},
		{"модератор: снять город", modActor, "moderator", []uuid.UUID{c1, c2}, perm.Moderator, []uuid.UUID{c1}, deny, ""},
		{"владелец: то же", owner, "moderator", []uuid.UUID{c1, c2}, perm.Moderator, []uuid.UUID{c2, c1}, same, ""},
		{"админ: та же роль поддержки", admin, "support", nil, perm.Support, []uuid.UUID{}, same, ""},
	}
	for _, tc := range cases {
		m := target(tc.from, tc.fromC...)
		before := e.acct(m.ID)
		ap, err := e.app.ChangeRole(ctx, tc.by, m.ID, tc.to, tc.toC, metaWeb)
		after := e.acct(m.ID)
		switch tc.want {
		case now:
			if err != nil || ap != nil {
				t.Fatalf("%s: %+v, %v", tc.name, ap, err)
			}
			if after.Version != before.Version+1 || !slices.Equal(e.citiesOf(m.ID), sortedIDs(tc.toC...)) {
				t.Fatalf("%s: не применено: %+v → %+v", tc.name, before, after)
			}
		case ask:
			if err != nil || ap == nil || ap.Action != "staff.role_change" || ap.Permission != tc.wantPerm ||
				ap.TargetID != m.ID || ap.Status != approvals.StatusPending {
				t.Fatalf("%s: %+v, %v", tc.name, ap, err)
			}
			if after != before {
				t.Fatalf("%s: запрос изменил учётку", tc.name)
			}
		case deny:
			if ap != nil {
				t.Fatalf("%s: запрос %+v", tc.name, ap)
			}
			wantForbidden(t, err)
		case same:
			if err != nil || ap != nil || after != before {
				t.Fatalf("%s: %+v, %v (%+v → %+v)", tc.name, ap, err, before, after)
			}
		}
	}
	if n := len(e.audits("staff.role_changed")); n != 2 {
		t.Fatalf("аудит смен сразу %d, ждали 2", n)
	}
}

// Поля и цель смены роли: роль — из списка; модератор без городов — 400 min_items; города у других
// ролей — 400 max_items; несуществующий город — 422; над собой — staff.self_action; нет учётки —
// staff.not_found; выключенная — 403 (вернуть её — приглашением).
func TestChangeRoleValidation(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	om := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	owner := e.as(om)
	m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c}})
	off := e.addMember("off@wf.example", memberOpts{Role: "support", Status: "disabled"})
	_, err := e.app.ChangeRole(ctx, owner, m.ID, perm.Role("boss"), nil, metaWeb)
	wantFieldError(t, err, "body.role", "enum")
	_, err = e.app.ChangeRole(ctx, owner, m.ID, perm.Moderator, []uuid.UUID{}, metaWeb)
	wantFieldError(t, err, "body.city_ids", "min_items")
	_, err = e.app.ChangeRole(ctx, owner, m.ID, perm.Support, []uuid.UUID{c}, metaWeb)
	wantFieldError(t, err, "body.city_ids", "max_items")
	if _, err = e.app.ChangeRole(ctx, owner, m.ID, perm.Moderator, []uuid.UUID{c, uuid.New()}, metaWeb); !errors.Is(err, staff.ErrCityNotFound) {
		t.Fatalf("город: %v", err)
	}
	if _, err = e.app.ChangeRole(ctx, owner, om.ID, perm.Admin, nil, metaWeb); !errors.Is(err, staff.ErrSelfAction) {
		t.Fatalf("над собой: %v", err)
	}
	if _, err = e.app.ChangeRole(ctx, owner, uuid.New(), perm.Admin, nil, metaWeb); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	_, err = e.app.ChangeRole(ctx, owner, off.ID, perm.Moderator, []uuid.UUID{c}, metaWeb)
	wantForbidden(t, err)
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("запросов %d", n)
	}
}

// Снятие городов сразу: версия +1, города, аудит staff.role_changed (было/стало), событие
// staff.role_revoked с removed_city_ids; ожидающие запросы сотрудника погашены.
func TestChangeRoleRevokeNow(t *testing.T) {
	e := newManageEnv(t)
	c1, c2 := e.city("kazan"), e.city("perm")
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	// цель — админ, у которого есть ожидающий запрос; смена admin → … всегда вдвоём, поэтому запрос
	// гасит исполнение; здесь — модератор (запросов у модератора нет, гашение — в TestChangeRoleExecute)
	m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c1, c2}})
	v := e.acct(m.ID).Version
	if ap, err := e.app.ChangeRole(context.Background(), owner, m.ID, perm.Moderator, []uuid.UUID{c2}, metaWeb); err != nil || ap != nil {
		t.Fatalf("%+v %v", ap, err)
	}
	if got := e.acct(m.ID); got.Version != v+1 || got.Role != "moderator" {
		t.Fatalf("учётка %+v", got)
	}
	au := e.audits("staff.role_changed")
	if len(au) != 1 || !strings.Contains(au[0].After, c2.String()) || strings.Contains(au[0].After, c1.String()) {
		t.Fatalf("аудит %+v", au)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2
		AND payload->'removed_city_ids' @> to_jsonb(ARRAY[$3::text])`, staff.EventRoleRevoked, m.ID, c1.String()); n != 1 {
		t.Fatal("событие staff.role_revoked с removed_city_ids")
	}
	e.wantAuditMeta("staff.role_changed")
}

// Исполнение смены роли: роль и города, версия +1, событие staff.role_granted, аудит от
// подтвердившего; ожидающие запросы сотрудника (как инициатора) погашены.
func TestChangeRoleExecute(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	pendingX, err := e.app.Invite(ctx, e.as(x), staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	v := e.acct(x.ID).Version
	ap, err := e.app.ChangeRole(ctx, a, x.ID, perm.Moderator, []uuid.UUID{c}, metaWeb)
	if err != nil || ap == nil || ap.Permission != "staff.roles.grant_elevated" {
		t.Fatalf("%+v %v", ap, err)
	}
	e.mustApprove(b, ap.ID)
	got := e.acct(x.ID)
	if got.Role != "moderator" || got.Version != v+1 || !slices.Equal(e.citiesOf(x.ID), []uuid.UUID{c}) {
		t.Fatalf("учётка %+v", got)
	}
	if st, errText := e.apState(pendingX.ID); st != approvals.StatusCancelled || errText != approvals.ErrorInitiatorChanged {
		t.Fatalf("запрос сотрудника: %s %s", st, errText)
	}
	if e.events(staff.EventRoleGranted, x.ID) != 1 {
		t.Fatal("событие staff.role_granted")
	}
	au := e.audits("staff.role_changed")
	if len(au) != 1 || au[0].StaffID == nil || *au[0].StaffID != b.StaffID {
		t.Fatalf("аудит %+v", au)
	}
}

// Устаревший payload: цель изменилась после запроса (смена роли сразу) — исполнение failed
// (platform.approval_stale), ничего не применено.
func TestChangeRoleStale(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c1, c2, c3 := e.city("kazan"), e.city("perm"), e.city("ufa")
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c1, c2}})
	ap, err := e.app.ChangeRole(ctx, a, m.ID, perm.Moderator, []uuid.UUID{c1, c2, c3}, metaWeb)
	if err != nil || ap == nil {
		t.Fatalf("%+v %v", ap, err)
	}
	if _, err := e.app.ChangeRole(ctx, a, m.ID, perm.Moderator, []uuid.UUID{c1}, metaWeb); err != nil {
		t.Fatal(err)
	}
	res, err := e.approve(b, ap.ID)
	// цель изменилась — причина platform.approval_stale, не initiator_changed (m1 уборки №6)
	if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || res.Error != httpx.CodeApprovalStale {
		t.Fatalf("%+v %v", res, err)
	}
	if cs := e.citiesOf(m.ID); !slices.Equal(cs, []uuid.UUID{c1}) {
		t.Fatalf("города %v", cs)
	}
}

// LockOut цели поднимает версию (решение контроллера): ожидающий запрос о ней устаревает — ErrStale.
func TestLockOutTargetStale(t *testing.T) {
	e := newManageEnv(t)
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	ap, err := e.app.ChangeRole(context.Background(), a, x.ID, perm.Support, nil, metaWeb)
	if err != nil || ap == nil {
		t.Fatalf("%+v %v", ap, err)
	}
	e.lockedSession(x) // пять неудач второго фактора — LockOut
	res, err := e.approve(b, ap.ID)
	if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || e.acct(x.ID).Role != "admin" {
		t.Fatalf("%+v %v", res, err)
	}
}
