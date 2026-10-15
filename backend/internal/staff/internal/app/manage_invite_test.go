package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/staff"
)

// Приглашение (P13, §4.3): всегда запрос (202); право — grant_elevated для owner/admin, иначе grant;
// без права — 403 до обращения к почте.
func TestInviteRights(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	admin := e.as(e.addMember("admin@wf.example", memberOpts{Role: "admin"}))
	mod := e.as(e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c}}))
	sup := e.as(e.addMember("sup@wf.example", memberOpts{Role: "support"}))
	cases := []struct {
		name string
		by   *auth.Staff
		role string
		perm string // "" — 403
	}{
		{"владелец → админ", owner, "admin", "staff.roles.grant_elevated"},
		{"владелец → владелец", owner, "owner", "staff.roles.grant_elevated"},
		{"владелец → модератор", owner, "moderator", "staff.roles.grant"},
		{"админ → поддержка", admin, "support", "staff.roles.grant"},
		{"админ → модератор", admin, "moderator", "staff.roles.grant"},
		{"админ → админ", admin, "admin", ""},
		{"админ → владелец", admin, "owner", ""},
		{"модератор → поддержка", mod, "support", ""},
		{"поддержка → поддержка", sup, "support", ""},
	}
	for i, tc := range cases {
		in := staff.InviteInput{Email: "new" + strconv.Itoa(i) + "@wf.example", DisplayName: "Новый", Role: tc.role}
		if tc.role == "moderator" {
			in.CityIDs = []uuid.UUID{c}
		}
		ap, err := e.app.Invite(ctx, tc.by, in, metaWeb)
		if tc.perm == "" {
			wantForbidden(t, err)
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if ap.Action != "staff.invite" || ap.Permission != tc.perm || ap.Status != approvals.StatusPending ||
			ap.RequestedBy != tc.by.StaffID || ap.TargetID != uuid.Nil {
			t.Fatalf("%s: запрос %+v", tc.name, ap)
		}
	}
	// запросы ничего не создали
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email LIKE 'new%'`); n != 0 {
		t.Fatalf("учёток до подтверждения %d", n)
	}
}

// Почта занята действующей (active) или приглашённой (invited) учёткой — 409 staff.email_taken на этапе
// запроса, в любом регистре; запроса нет.
func TestInviteEmailTaken(t *testing.T) {
	e := newManageEnv(t)
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	e.addMember("active@wf.example", memberOpts{Role: "support"})
	e.invitee("invited@wf.example", memberOpts{Role: "support"}, purposeInvite)
	for _, email := range []string{"active@wf.example", " INVITED@wf.example ", "owner@wf.example"} {
		_, err := e.app.Invite(context.Background(), owner, staff.InviteInput{Email: email, DisplayName: "Х", Role: "support"}, metaWeb)
		if !errors.Is(err, staff.ErrEmailTaken) {
			t.Fatalf("%s: %v", email, err)
		}
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("запросов %d", n)
	}
}

// Поля приглашения: почта и имя — как у bootstrap; роль — из списка; города — только у модератора
// (хотя бы один, не больше 100), несуществующий — 422 staff.city_not_found.
func TestInviteValidation(t *testing.T) {
	e := newManageEnv(t)
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	c := e.city("kazan")
	many := make([]uuid.UUID, 101)
	for i := range many {
		many[i] = uuid.New()
	}
	cases := []struct {
		name        string
		in          staff.InviteInput
		field, rule string
	}{
		{"почта", staff.InviteInput{Email: "не почта", DisplayName: "Х", Role: "support"}, "body.email", "format"},
		{"имя пустое", staff.InviteInput{Email: "x@wf.example", DisplayName: "  ", Role: "support"}, "body.display_name", "minLength"},
		{"имя длинное", staff.InviteInput{Email: "x@wf.example", DisplayName: strings.Repeat("я", 81), Role: "support"}, "body.display_name", "maxLength"},
		{"роль", staff.InviteInput{Email: "x@wf.example", DisplayName: "Х", Role: "boss"}, "body.role", "enum"},
		{"модератор без городов", staff.InviteInput{Email: "x@wf.example", DisplayName: "Х", Role: "moderator"}, "body.city_ids", "min_items"},
		{"поддержка с городом", staff.InviteInput{Email: "x@wf.example", DisplayName: "Х", Role: "support", CityIDs: []uuid.UUID{c}}, "body.city_ids", "max_items"},
		{"101 город", staff.InviteInput{Email: "x@wf.example", DisplayName: "Х", Role: "moderator", CityIDs: many}, "body.city_ids", "max_items"},
	}
	for _, tc := range cases {
		_, err := e.app.Invite(context.Background(), owner, tc.in, metaWeb)
		wantFieldError(t, err, tc.field, tc.rule)
	}
	_, err := e.app.Invite(context.Background(), owner, staff.InviteInput{Email: "x@wf.example", DisplayName: "Х",
		Role: "moderator", CityIDs: []uuid.UUID{c, uuid.New()}}, metaWeb)
	if !errors.Is(err, staff.ErrCityNotFound) {
		t.Fatalf("несуществующий город: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("запросов %d", n)
	}
}

// Исполнение staff.invite под ролью БД admin (как в проде): учётка invited с ролью и городами
// модератора (отложенный триггер и права grants.sql), created_by и granted_by — подтвердивший;
// приглашение и письмо staff.invite; аудит staff.invited от подтвердившего; событие staff.invited.
func TestInviteExecute(t *testing.T) {
	e := newManageEnv(t)
	c1, c2 := e.city("kazan"), e.city("perm")
	a := e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	b := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	ap, err := e.app.Invite(context.Background(), e.as(a), staff.InviteInput{
		Email: " Ivan.P@WF.example ", DisplayName: " Иван П. ", Role: "moderator", CityIDs: []uuid.UUID{c2, c1, c2},
	}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(ap.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["email"] != "ivan.p@wf.example" || p["display_name"] != "Иван П." || p["role"] != "moderator" {
		t.Fatalf("payload %s", ap.Payload)
	}
	e.mustApprove(e.as(b), ap.ID)
	id := e.accountID("ivan.p@wf.example")
	got := e.acct(id)
	if got.Status != "invited" || got.Role != "moderator" || got.Name != "Иван П." || got.CreatedBy == nil || *got.CreatedBy != b.ID {
		t.Fatalf("учётка %+v", got)
	}
	if n := e.count(`SELECT count(*) FROM staff_roles WHERE staff_id = $1 AND granted_by = $2`, id, b.ID); n != 1 {
		t.Fatal("granted_by — подтвердивший")
	}
	if cs := e.citiesOf(id); len(cs) != 2 || !slices.Equal(cs, sortedIDs(c1, c2)) {
		t.Fatalf("города %v", cs)
	}
	inv := e.liveInvite(id)
	if inv.Purpose != purposeInvite || e.mails(staff.MailInvite, inv.ID, id) != 1 {
		t.Fatalf("приглашение %+v", inv)
	}
	au := e.audits("staff.invited")
	if len(au) != 1 || au[0].StaffID == nil || *au[0].StaffID != b.ID || au[0].ObjectID == nil || *au[0].ObjectID != id ||
		!strings.Contains(au[0].After, a.ID.String()) {
		t.Fatalf("аудит %+v", au)
	}
	if e.events(staff.EventInvited, id) != 1 {
		t.Fatal("событие staff.invited")
	}
}

// Почта выключенной учётки (§2.6): запрос несёт existing_id (цель — эта учётка), исполнение — сброс
// доступа той же учётки с новой ролью, городами и именем: пароль, второй фактор, коды и сессии стёрты,
// статус invited, новое приглашение, версия выросла; история (id) сохраняется.
func TestInviteDisabledReturnsSameAccount(t *testing.T) {
	e := newManageEnv(t)
	c := e.city("kazan")
	a := e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	b := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	x := e.addMember("x@wf.example", memberOpts{Role: "admin", Status: "disabled", Name: "Старое имя"})
	_, sid := e.rawSession(x.ID, "active", ipHome, "UA", t0)
	before := e.acct(x.ID)
	ap, err := e.app.Invite(context.Background(), e.as(a), staff.InviteInput{
		Email: "X@wf.example", DisplayName: "Новое имя", Role: "moderator", CityIDs: []uuid.UUID{c},
	}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if ap.TargetID != x.ID || ap.Permission != "staff.roles.grant" || !strings.Contains(string(ap.Payload), x.ID.String()) {
		t.Fatalf("запрос %+v %s", ap, ap.Payload)
	}
	// цель запроса не подтверждает (правило двух рук) — подтверждает второй владелец
	e.mustApprove(e.as(b), ap.ID)
	got := e.acct(x.ID)
	if got.Status != "invited" || got.Role != "moderator" || got.Name != "Новое имя" || got.HasPassword ||
		got.Version <= before.Version {
		t.Fatalf("учётка %+v (было %+v)", got, before)
	}
	if e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'x@wf.example'`) != 1 ||
		e.count(`SELECT count(*) FROM staff_mfa WHERE staff_id = $1`, x.ID) != 0 ||
		e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1`, x.ID) != 0 {
		t.Fatal("учётка не та же или второй фактор не стёрт")
	}
	if cs := e.citiesOf(x.ID); len(cs) != 1 || cs[0] != c {
		t.Fatalf("города %v", cs)
	}
	if e.revokedReason(sid) == "" {
		t.Fatal("сессия не погашена")
	}
	inv := e.liveInvite(x.ID)
	if e.mails(staff.MailInvite, inv.ID, x.ID) != 1 {
		t.Fatal("письмо приглашения")
	}
	if e.events(staff.EventInvited, x.ID) != 1 || len(e.audits("staff.invited")) != 1 {
		t.Fatal("событие или аудит")
	}
}

// Гонка двух исполнений приглашения на одну почту (P13): два запроса, два подтверждающих стартуют
// вместе — ровно одно executed, второе failed (staff.email_taken), учётка одна, частичных записей
// второго нет (savepoint).
func TestInviteConcurrentSameEmail(t *testing.T) {
	e := newManageEnv(t)
	a := e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	c := e.as(e.addMember("owner.c@wf.example", memberOpts{Role: "owner"}))
	sa := e.as(a)
	in := staff.InviteInput{Email: "race@wf.example", DisplayName: "Гонка", Role: "support"}
	r1, err := e.app.Invite(context.Background(), sa, in, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.app.Invite(context.Background(), sa, in, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	var taken, done atomic.Int32
	barrier(2, func(i int) {
		st, apID := b, r1.ID
		if i == 1 {
			st, apID = c, r2.ID
		}
		ap, err := e.approve(st, apID)
		switch {
		case err == nil && ap.Status == approvals.StatusExecuted:
			done.Add(1)
		case errors.Is(err, staff.ErrEmailTaken) && ap.Status == approvals.StatusFailed && ap.Error == staff.CodeEmailTaken:
			taken.Add(1)
		default:
			t.Errorf("исход %+v, %v", ap, err)
		}
	})
	if done.Load() != 1 || taken.Load() != 1 {
		t.Fatalf("executed %d, failed %d", done.Load(), taken.Load())
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'race@wf.example'`); n != 1 {
		t.Fatalf("учёток %d", n)
	}
	if n := len(e.audits("staff.invited")); n != 1 {
		t.Fatalf("аудит staff.invited %d", n)
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail' AND args->>'kind' = 'staff.invite'`); n != 1 {
		t.Fatalf("писем %d", n)
	}
}
