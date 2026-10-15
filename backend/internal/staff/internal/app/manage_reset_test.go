package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/staff"
)

// Сброс доступа — всегда запрос: reset_elevated для владельца и админа, reset для модератора и
// поддержки; без права — 403; над собой — self_action; почта не совпала — mismatch; выключенная —
// 403 (вернуть её — приглашением).
func TestResetAccessRequest(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	c := e.city("kazan")
	om := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	owner := e.as(om)
	am := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	admin := e.as(am)
	mod := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c}})
	admin2 := e.addMember("admin2@wf.example", memberOpts{Role: "admin"})
	off := e.addMember("off@wf.example", memberOpts{Role: "support", Status: "disabled"})
	ap, err := e.app.ResetAccess(ctx, owner, admin2.ID, "security", "", admin2.Email, metaWeb)
	if err != nil || ap.Action != "staff.access_reset" || ap.Permission != "staff.access.reset_elevated" || ap.TargetID != admin2.ID {
		t.Fatalf("владелец → админ: %+v %v", ap, err)
	}
	ap, err = e.app.ResetAccess(ctx, admin, mod.ID, "security", "", mod.Email, metaWeb)
	if err != nil || ap.Permission != "staff.access.reset" {
		t.Fatalf("админ → модератор: %+v %v", ap, err)
	}
	_, err = e.app.ResetAccess(ctx, admin, admin2.ID, "security", "", admin2.Email, metaWeb)
	wantForbidden(t, err)
	_, err = e.app.ResetAccess(ctx, e.as(mod), am.ID, "security", "", am.Email, metaWeb)
	wantForbidden(t, err)
	if _, err = e.app.ResetAccess(ctx, owner, om.ID, "security", "", om.Email, metaWeb); !errors.Is(err, staff.ErrSelfAction) {
		t.Fatalf("над собой: %v", err)
	}
	_, err = e.app.ResetAccess(ctx, owner, mod.ID, "security", "", "admin@wf.example", metaWeb)
	wantFieldError(t, err, "body.confirm", "mismatch")
	_, err = e.app.ResetAccess(ctx, owner, off.ID, "security", "", off.Email, metaWeb)
	wantForbidden(t, err)
	if _, err = e.app.ResetAccess(ctx, owner, uuid.New(), "security", "", "x@wf.example", metaWeb); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 2 {
		t.Fatalf("запросов %d", n)
	}
}

// Исполнение сброса: пароль, второй фактор и коды стёрты, статус invited, все сессии погашены
// (access_reset), версия +1, ожидающие запросы сотрудника погашены, новое приглашение access_reset с
// письмом, аудит и событие staff.access_reset; новый код принимается.
func TestResetAccessExecute(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	sx := e.as(x)
	pending, err := e.app.Invite(ctx, sx, staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	v := e.acct(x.ID).Version
	ap, err := e.app.ResetAccess(ctx, owner, x.ID, "security", "потерял телефон", x.Email, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	e.mustApprove(b, ap.ID)
	got := e.acct(x.ID)
	if got.Status != "invited" || got.HasPassword || got.Version != v+1 || got.Role != "admin" {
		t.Fatalf("учётка %+v", got)
	}
	if e.count(`SELECT count(*) FROM staff_mfa WHERE staff_id = $1`, x.ID) != 0 ||
		e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1`, x.ID) != 0 {
		t.Fatal("второй фактор не стёрт")
	}
	if e.revokedReason(sx.SessionID) != "access_reset" {
		t.Fatal("сессия не погашена как access_reset")
	}
	if st, _ := e.apState(pending.ID); st != approvals.StatusCancelled {
		t.Fatalf("запрос сотрудника %s", st)
	}
	inv := e.liveInvite(x.ID)
	if inv.Purpose != purposeReset || e.mails(staff.MailAccessReset, inv.ID, x.ID) != 1 {
		t.Fatalf("приглашение %+v", inv)
	}
	if len(e.audits("staff.access_reset")) != 1 || e.events(staff.EventAccessReset, x.ID) != 1 {
		t.Fatal("аудит или событие")
	}
	e.accept(x, e.currentCode(x.ID))
}

// Последний активный владелец: сброс доступа и выключение оператором — staff.last_owner; оператору
// сброс доступа единственному владельцу разрешён (выход из тупика, §4.2).
func TestOperatorLastOwner(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	o := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	e.addMember("owner.off@wf.example", memberOpts{Role: "owner", Status: "disabled"})
	e.invitee("owner.inv@wf.example", memberOpts{Role: "owner"}, purposeInvite)
	if err := e.app.OperatorDisable(ctx, o.Email); !errors.Is(err, staff.ErrLastOwner) {
		t.Fatalf("выключение последнего: %v", err)
	}
	if e.acct(o.ID).Status != "active" {
		t.Fatal("выключен")
	}
	if err := e.app.OperatorDisable(ctx, "nobody@wf.example"); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	if err := e.app.OperatorDisable(ctx, "owner.off@wf.example"); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("уже выключен: %v", err)
	}
	// приглашённого (не активного) владельца выключить можно — последний активный остаётся
	if err := e.app.OperatorDisable(ctx, "owner.inv@wf.example"); err != nil {
		t.Fatal(err)
	}
	code, err := e.app.OperatorResetAccess(ctx, " OWNER@wf.example ")
	if err != nil || !inviteCodeFormat.MatchString(code) {
		t.Fatalf("сброс единственному: %q %v", code, err)
	}
	if got := e.acct(o.ID); got.Status != "invited" || got.HasPassword {
		t.Fatalf("учётка %+v", got)
	}
	au := e.audits("staff.access_reset")
	if len(au) != 1 || au[0].Role != "operator" || au[0].StaffID != nil {
		t.Fatalf("аудит %+v", au)
	}
	if _, err := e.app.OperatorResetAccess(ctx, "nobody@wf.example"); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	e.accept(o, code)
}
