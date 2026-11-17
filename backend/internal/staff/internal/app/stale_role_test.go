package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
)

// Роль сотрудника сменили, пока его действие ждало замок учётки (ревью Task 4 плана I-4, тот же класс, что
// TestStaffLinkedPlayerTxSeesLinkCommittedWhileWaiting): при READ COMMITTED оператор «FOR SHARE/UPDATE OF a + JOIN
// staff_roles» после ожидания перечитывает только строку учётки, а роль берёт из снимка до ожидания. Держатель —
// как смена роли под замком: учётка FOR UPDATE, новая роль, версия + 1, коммит после того, как действие встало на
// замок. Действие обязано увидеть новую роль.

// changeRoleWhileWaiting — держатель меняет роль сотрудника id на role под замком его учётки; act стартует, встаёт
// на замок (барьер — pg_stat_activity), держатель коммитит; ответ — ошибка act.
func (e *env) changeRoleWhileWaiting(id uuid.UUID, role string, act func() error) error {
	e.t.Helper()
	ctx := context.Background()
	hold, err := e.owner.Begin(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	for _, q := range []string{
		`SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`,
		`UPDATE staff_roles SET role = $2::staff_role WHERE staff_id = $1`,
		`UPDATE staff_accounts SET version = version + 1 WHERE id = $1`,
	} {
		args := []any{id}
		if q == `UPDATE staff_roles SET role = $2::staff_role WHERE staff_id = $1` {
			args = append(args, role)
		}
		if _, err := hold.Exec(ctx, q, args...); err != nil {
			e.t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- act() }()
	e.waitLockWaiters(1)
	if err := hold.Commit(ctx); err != nil {
		e.t.Fatal(err)
	}
	return <-done
}

// Хук ActorLock подтверждений (lockActors → checkActor): владельца-подтверждающего понизили до support, пока
// подтверждение ждало его учётку, — 403, запрос pending, исполнитель не вызван.
func TestStaleRoleActorLockApprover(t *testing.T) {
	t.Parallel()
	e := newNoopEnv(t)
	a := e.as(e.addMember("admin.a@wf.example", memberOpts{Role: "admin"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	ap := e.request(a)
	err := e.changeRoleWhileWaiting(b.StaffID, "support", func() error {
		_, err := e.approve(b, ap.ID)
		return err
	})
	wantForbidden(t, err)
	if st, _ := e.apState(ap.ID); st != approvals.StatusPending || e.execs.Load() != 0 {
		t.Fatalf("запрос %s, исполнитель %d", st, e.execs.Load())
	}
}

// PutPlayerLink / DeletePlayerLink (lockAccounts → checkActor): владельца-автора понизили до admin, пока действие
// ждало его учётку, — 403, связь не изменилась.
func TestStaleRolePlayerLink(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	e.addMember("owner.keep@wf.example", memberOpts{Role: "owner"})
	target := e.addMember("t@wf.example", memberOpts{Role: "support"})
	linked := e.player("stale_linked")
	e.exec(`INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at) VALUES ($1, $2, $1, now())`,
		target.ID, linked)
	p := e.player("stale_p")
	v0 := e.acct(target.ID).Version
	for _, op := range []string{"put", "delete"} {
		s := e.as(e.addMember("owner."+op+"@wf.example", memberOpts{Role: "owner"}))
		err := e.changeRoleWhileWaiting(s.StaffID, "admin", func() error {
			if op == "put" {
				_, err := e.app.PutPlayerLink(ctx, s, target.ID, p, metaWeb)
				return err
			}
			return e.app.DeletePlayerLink(ctx, s, target.ID, metaWeb)
		})
		wantForbidden(t, err)
	}
	if e.linkOf(target.ID) != linked || e.acct(target.ID).Version != v0 {
		t.Fatal("действие понижённого автора изменило связь")
	}
}

// ResendInvite (lockAccounts → checkActor): админа понизили до support (у support нет staff.invites.resend), пока
// повторная отправка ждала его учётку, — 403, нового приглашения нет.
func TestStaleRoleResendInvite(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	s := e.as(e.addMember("admin.r@wf.example", memberOpts{Role: "admin"}))
	m, _ := e.invitee("stale.invitee@wf.example", memberOpts{Role: "support"}, purposeReset)
	e.clock.Advance(2 * time.Minute) // пауза повторной отправки прошла
	err := e.changeRoleWhileWaiting(s.StaffID, "support", func() error {
		return e.app.ResendInvite(ctx, s, m.ID, metaWeb)
	})
	wantForbidden(t, err)
	if n := len(e.invites(m.ID)); n != 1 {
		t.Fatalf("приглашений %d, ждали 1", n)
	}
}

// LockAccount (своё действие — смена языка): роль в аудите — та, что после ожидания замка, а не из старого снимка.
func TestStaleRoleSelfAudit(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	m := e.addMember("self.audit@wf.example", memberOpts{Role: "admin"})
	s := e.as(m)
	if err := e.changeRoleWhileWaiting(m.ID, "support", func() error {
		return e.app.SetLocale(context.Background(), s, "en", metaWeb)
	}); err != nil {
		t.Fatal(err)
	}
	if got := e.audits("staff.locale_changed"); len(got) != 1 || got[0].Role != "support" {
		t.Fatalf("аудит смены языка: %+v, ждали роль support", got)
	}
}

// LockAccountByEmail (принятие приглашения): роль в аудите принятия — после ожидания замка учётки.
func TestStaleRoleInviteAcceptAudit(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	m, code := e.invitee("stale.accept@wf.example", memberOpts{Role: "admin"}, purposeInvite)
	if err := e.changeRoleWhileWaiting(m.ID, "support", func() error {
		_, err := e.app.AcceptInvite(context.Background(), m.Email, code, newPassword, nil, "", metaWeb)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := e.audits("staff.invite_accepted"); len(got) != 1 || got[0].Role != "support" {
		t.Fatalf("аудит принятия: %+v, ждали роль support", got)
	}
}

// Цель под FOR UPDATE (lockAccounts, ветка target): поддержку повысили до admin, пока выключение админом ждало её
// учётку, — право выключения считается по свежей роли цели (staff.roles.revoke_admin у admin — deny): 403, учётка
// активна. В проде смену роли и выключение сериализует ещё и lockOwners; держатель теста его не берёт — страж
// чтения роли цели после замка.
func TestStaleRoleDisableTarget(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	s := e.as(e.addMember("admin.d@wf.example", memberOpts{Role: "admin"}))
	x := e.addMember("sup.x@wf.example", memberOpts{Role: "support"})
	err := e.changeRoleWhileWaiting(x.ID, "admin", func() error {
		_, err := e.app.Disable(context.Background(), s, x.ID, "left_team", "", x.Email, metaWeb)
		return err
	})
	wantForbidden(t, err)
	if st := e.acct(x.ID).Status; st != "active" {
		t.Fatalf("статус цели %s", st)
	}
}
