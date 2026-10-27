package app_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/staff"
)

// Выключение: confirm — почта цели после нормализации (регистр, пробелы, IDN → xn--), иначе
// body.confirm mismatch; reason — из списка (body.reason enum); comment ≤ 500 (body.comment maxLength);
// над собой — self_action; нет учётки — not_found; уже выключена — wrong_stage.
func TestDisableValidation(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	om := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	owner := e.as(om)
	sup := e.addMember("ivan@xn--e1afmkfd.xn--p1ai", memberOpts{Role: "support"})
	off := e.addMember("off@wf.example", memberOpts{Role: "support", Status: "disabled"})
	_, err := e.app.Disable(ctx, owner, sup.ID, "left_team", "", "other@wf.example", metaWeb)
	wantFieldError(t, err, "body.confirm", "mismatch")
	_, err = e.app.Disable(ctx, owner, sup.ID, "left_team", "", "не почта", metaWeb)
	wantFieldError(t, err, "body.confirm", "mismatch")
	_, err = e.app.Disable(ctx, owner, sup.ID, "fired", "", sup.Email, metaWeb)
	wantFieldError(t, err, "body.reason", "enum")
	_, err = e.app.Disable(ctx, owner, sup.ID, "other", strings.Repeat("я", 501), sup.Email, metaWeb)
	wantFieldError(t, err, "body.comment", "maxLength")
	if _, err = e.app.Disable(ctx, owner, om.ID, "other", "", om.Email, metaWeb); !errors.Is(err, staff.ErrSelfAction) {
		t.Fatalf("над собой: %v", err)
	}
	if _, err = e.app.Disable(ctx, owner, uuid.New(), "other", "", "x@wf.example", metaWeb); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	if _, err = e.app.Disable(ctx, owner, off.ID, "other", "", off.Email, metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("уже выключена: %v", err)
	}
	if e.acct(sup.ID).Status != "active" {
		t.Fatal("выключена при отказе")
	}
	// регистр, пробелы и кириллический домен (U-форма) — та же почта
	if ap, err := e.app.Disable(ctx, owner, sup.ID, "other", "", "  IVAN@ПРИМЕР.РФ ", metaWeb); err != nil || ap != nil {
		t.Fatalf("IDN confirm: %+v %v", ap, err)
	}
	if e.acct(sup.ID).Status != "disabled" {
		t.Fatal("не выключена")
	}
}

// Право — по роли цели: модератор и поддержка — revoke (владелец, админ: сразу), админ — revoke_admin
// (владелец: сразу; админ — 403), владелец — revoke_owner (владелец: запрос; админ — 403);
// модератор — 403.
func TestDisableRights(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	admin := e.as(e.addMember("admin@wf.example", memberOpts{Role: "admin"}))
	mod := e.as(e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c}}))
	n := 0
	target := func(role string) member {
		n++
		o := memberOpts{Role: role}
		if role == "moderator" {
			o.Cities = []uuid.UUID{c}
		}
		return e.addMember("t"+strconv.Itoa(n)+"@wf.example", o)
	}
	cases := []struct {
		name, role string
		by         *auth.Staff
		want       string // now | ask | deny
	}{
		{"владелец → админ", "admin", owner, "now"},
		{"владелец → модератор", "moderator", owner, "now"},
		{"владелец → владелец", "owner", owner, "ask"},
		{"админ → поддержка", "support", admin, "now"},
		{"админ → модератор", "moderator", admin, "now"},
		{"админ → админ", "admin", admin, "deny"},
		{"админ → владелец", "owner", admin, "deny"},
		{"модератор → поддержка", "support", mod, "deny"},
	}
	for _, tc := range cases {
		m := target(tc.role)
		ap, err := e.app.Disable(ctx, tc.by, m.ID, "role_change", "", m.Email, metaWeb)
		status := e.acct(m.ID).Status
		switch tc.want {
		case "now":
			if err != nil || ap != nil || status != "disabled" {
				t.Fatalf("%s: %+v %v %s", tc.name, ap, err, status)
			}
		case "ask":
			if err != nil || ap == nil || ap.Action != "staff.revoke_owner" || ap.Permission != "staff.roles.revoke_owner" ||
				ap.TargetID != m.ID || status != "active" {
				t.Fatalf("%s: %+v %v %s", tc.name, ap, err, status)
			}
		case "deny":
			wantForbidden(t, err)
			if status != "active" {
				t.Fatalf("%s: %s", tc.name, status)
			}
		}
	}
}

// Исполнение выключения: статус disabled, версия +1, все сессии погашены (disabled), живое приглашение
// погашено, ожидающие запросы сотрудника погашены, аудит staff.disabled (причина «код: комментарий» —
// только в reason, не в after), событие staff.role_revoked; загрузчик больше не пускает.
func TestDisableEffects(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	sx := e.as(x)
	_, sid2 := e.rawSession(x.ID, "active", ipHome, "UA", t0)
	pending, err := e.app.Invite(ctx, sx, staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	inv, _ := e.invitee("inv@wf.example", memberOpts{Role: "support"}, purposeInvite)
	v := e.acct(x.ID).Version
	if _, err := e.app.Disable(ctx, owner, x.ID, "left_team", "  ушёл в другой проект ", "admin@wf.example", metaWeb); err != nil {
		t.Fatal(err)
	}
	if got := e.acct(x.ID); got.Status != "disabled" || got.Version != v+1 {
		t.Fatalf("учётка %+v", got)
	}
	if e.revokedReason(sid2) != "disabled" || e.revokedReason(sx.SessionID) != "disabled" {
		t.Fatal("сессии не погашены как disabled")
	}
	if st, errText := e.apState(pending.ID); st != approvals.StatusCancelled || errText != approvals.ErrorInitiatorChanged {
		t.Fatalf("запрос сотрудника %s %s", st, errText)
	}
	var reason, after string
	if err := e.owner.QueryRow(ctx, `SELECT reason, after::text FROM audit_log WHERE action = 'staff.disabled' AND object_id = $1`, x.ID).
		Scan(&reason, &after); err != nil {
		t.Fatal(err)
	}
	if reason != "left_team: ушёл в другой проект" || strings.Contains(after, "проект") {
		t.Fatalf("причина %q, after %s", reason, after)
	}
	e.wantAuditMeta("staff.disabled")
	if e.events(staff.EventRoleRevoked, x.ID) != 1 {
		t.Fatal("событие staff.role_revoked")
	}
	// приглашённого выключили — его живое приглашение погашено (код стёрт)
	if _, err := e.app.Disable(ctx, owner, inv.ID, "other", "", inv.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM staff_invites WHERE staff_id = $1 AND revoked_at IS NULL AND consumed_at IS NULL`, inv.ID); n != 0 {
		t.Fatal("приглашение живо")
	}
}

// Владельца выключают только вдвоём (revoke_owner): запрос с причиной, исполнение вторым владельцем;
// письма U1 — только о выключении админа (здесь нет).
func TestDisableOwnerViaApproval(t *testing.T) {
	e := newManageEnv(t)
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	c := e.as(e.addMember("owner.c@wf.example", memberOpts{Role: "owner"}))
	ap, err := e.app.Disable(context.Background(), a, b.ID, "security", "утечка", " OWNER.B@wf.example", metaWeb)
	if err != nil || ap == nil {
		t.Fatalf("%+v %v", ap, err)
	}
	if strings.Contains(e.audits("platform.approval_requested")[0].After, "утечка") {
		t.Fatal("комментарий в аудите запроса")
	}
	e.mustApprove(c, ap.ID)
	if e.acct(b.ID).Status != "disabled" {
		t.Fatal("не выключен")
	}
	var reason string
	if err := e.owner.QueryRow(context.Background(), `SELECT reason FROM audit_log WHERE action = 'staff.disabled'`).Scan(&reason); err != nil || reason != "security: утечка" {
		t.Fatalf("%q %v", reason, err)
	}
	if n := e.mails(staff.MailAdminDisabled, uuid.Nil, uuid.Nil); n != 0 {
		t.Fatalf("писем U1 %d", n)
	}
}

// U1 (сценарий): выключение админа владельцем A — письма staff.admin_disabled владельцам B и C (ref —
// id записи аудита staff.disabled), не A (инициатор) и не выключенному D; выключение модератора — писем
// нет; OperatorDisable админа — всем активным владельцам.
func TestDisableAdminMailsOwners(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	a := e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	b := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	cm := e.addMember("owner.c@wf.example", memberOpts{Role: "owner"})
	e.addMember("owner.d@wf.example", memberOpts{Role: "owner", Status: "disabled"})
	x := e.addMember("admin.x@wf.example", memberOpts{Role: "admin"})
	y := e.addMember("admin.y@wf.example", memberOpts{Role: "admin"})
	mod := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c}})
	sa := e.as(a)

	if _, err := e.app.Disable(ctx, sa, mod.ID, "other", "", mod.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	if n := e.mails(staff.MailAdminDisabled, uuid.Nil, uuid.Nil); n != 0 {
		t.Fatalf("модератор: писем %d", n)
	}
	if _, err := e.app.Disable(ctx, sa, x.ID, "security", "секрет", x.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	ref := e.disabledAudit(x.ID)
	if e.mails(staff.MailAdminDisabled, ref, b.ID) != 1 || e.mails(staff.MailAdminDisabled, ref, cm.ID) != 1 ||
		e.mails(staff.MailAdminDisabled, uuid.Nil, uuid.Nil) != 2 {
		t.Fatal("письма U1: B и C")
	}
	if e.count(`SELECT count(*) FROM river_job WHERE args::text LIKE '%секрет%' OR args::text LIKE '%admin.x%'`) != 0 {
		t.Fatal("комментарий или почта в аргументах задачи")
	}
	if err := e.app.OperatorDisable(ctx, " Admin.Y@wf.example"); err != nil {
		t.Fatal(err)
	}
	ref = e.disabledAudit(y.ID)
	for _, o := range []member{a, b, cm} {
		if e.mails(staff.MailAdminDisabled, ref, o.ID) != 1 {
			t.Fatalf("оператор: письма владельцу %s", o.Email)
		}
	}
	if n := e.mails(staff.MailAdminDisabled, ref, uuid.Nil); n != 3 {
		t.Fatalf("оператор: писем %d", n)
	}
}

// disabledAudit — id записи аудита staff.disabled учётки (ref письма U1).
func (e *menv) disabledAudit(staffID uuid.UUID) uuid.UUID {
	e.t.Helper()
	var ref uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM audit_log WHERE action = 'staff.disabled' AND object_id = $1`, staffID).Scan(&ref); err != nil {
		e.t.Fatal(err)
	}
	return ref
}
