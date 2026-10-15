package app_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

// activeOwners — сколько активных владельцев.
func (e *menv) activeOwners() int {
	return e.count(`SELECT count(*) FROM staff_accounts a JOIN staff_roles r ON r.staff_id = a.id
		WHERE a.status = 'active' AND r.role = 'owner'`)
}

// Гонка двух снятий из двух владельцев (D17, advisory lock): два выключения стартуют вместе — одно
// проходит, второе — staff.last_owner; активный владелец остаётся ровно один. -count=30.
func TestLastOwnerConcurrent(t *testing.T) {
	e := newManageEnv(t)
	a := e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	b := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	var ok, last atomic.Int32
	barrier(2, func(i int) {
		email := a.Email
		if i == 1 {
			email = b.Email
		}
		switch err := e.app.OperatorDisable(context.Background(), email); {
		case err == nil:
			ok.Add(1)
		case errors.Is(err, staff.ErrLastOwner):
			last.Add(1)
		default:
			t.Errorf("выключение %s: %v", email, err)
		}
	})
	if ok.Load() != 1 || last.Load() != 1 || e.activeOwners() != 1 {
		t.Fatalf("прошло %d, last_owner %d, активных владельцев %d", ok.Load(), last.Load(), e.activeOwners())
	}
}

// Гонка исполнения подтверждения и операторских выключений над тремя владельцами: что бы ни прошло,
// активный владелец остаётся; без взаимных блокировок и 500.
func TestLastOwnerConcurrentMixed(t *testing.T) {
	e := newManageEnv(t)
	a := e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	b := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	c := e.addMember("owner.c@wf.example", memberOpts{Role: "owner"})
	sb, sc := e.as(b), e.as(c)
	ap, err := e.app.Disable(context.Background(), sb, a.ID, "other", "", a.Email, metaWeb)
	if err != nil || ap == nil {
		t.Fatalf("%+v %v", ap, err)
	}
	barrier(3, func(i int) {
		var err error
		switch i {
		case 0:
			_, err = e.approve(sc, ap.ID)
		case 1:
			err = e.app.OperatorDisable(context.Background(), b.Email)
		case 2:
			err = e.app.OperatorDisable(context.Background(), c.Email)
		}
		if err != nil && !errors.Is(err, staff.ErrLastOwner) && !errors.Is(err, approvals.ErrStale) &&
			!errors.Is(err, approvals.ErrClosed) && !errors.Is(err, auth.ErrSessionInvalid) {
			t.Errorf("исход %d: %v", i, err)
		}
	})
	if n := e.activeOwners(); n < 1 {
		t.Fatalf("активных владельцев %d", n)
	}
}

// Проверка инициатора (D17): инициатор выключен до подтверждения — запрос погашен; если гашение его
// пропустило (строку держал откатывающийся approve), подтверждение — failed (ErrStale), ничего не
// создано.
func TestInitiatorDisabledFails(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	ap, err := e.app.Invite(ctx, e.as(x), staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	release := e.holdApproval(ap.ID)
	if _, err := e.app.Disable(ctx, owner, x.ID, "other", "", x.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	release()
	if st, _ := e.apState(ap.ID); st != approvals.StatusPending {
		t.Fatalf("гашение не пропустило строку под замком: %s", st)
	}
	res, err := e.approve(b, ap.ID)
	if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed {
		t.Fatalf("%+v %v", res, err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'n@wf.example'`); n != 0 {
		t.Fatal("учётка создана")
	}
}

// I1 (ревью 8a) на настоящих staff_accounts: запрос админа X; строку держит откатывающийся approve,
// X сбрасывают доступ (гашение пропускает запрос), X снова активен с той же ролью (принятие, 2FA, ack);
// подтверждение — failed: версия X выросла после запроса. Контроль — свежий запрос того же X
// исполняется.
func TestInitiatorReactivatedFails(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	ap, err := e.app.Invite(ctx, e.as(x), staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	release := e.holdApproval(ap.ID)
	reset, err := e.app.ResetAccess(ctx, a, x.ID, "security", "", x.Email, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	e.mustApprove(b, reset.ID)
	release()
	_, setup := e.accept(x, e.currentCode(x.ID))
	_, tok := e.completeSetup(setup)
	sx := e.load(tok)
	if e.acct(x.ID).Status != "active" || sx.Role != "admin" {
		t.Fatal("X не активен снова")
	}
	res, err := e.approve(b, ap.ID)
	if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || res.Error != approvals.ErrorInitiatorChanged {
		t.Fatalf("%+v %v", res, err)
	}
	fresh, err := e.app.Invite(ctx, sx, staff.InviteInput{Email: "m@wf.example", DisplayName: "М", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	e.mustApprove(b, fresh.ID)
}

// Инициатор понижен до исполнения (запрос пропущен гашением): grant у поддержки нет — failed.
func TestInitiatorDemotedFails(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	ap, err := e.app.Invite(ctx, e.as(x), staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	release := e.holdApproval(ap.ID)
	demote, err := e.app.ChangeRole(ctx, a, x.ID, perm.Support, nil, metaWeb)
	if err != nil || demote == nil {
		t.Fatalf("%+v %v", demote, err)
	}
	e.mustApprove(b, demote.ID)
	release()
	if res, err := e.approve(b, ap.ID); !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed {
		t.Fatalf("%+v %v", res, err)
	}
}

// Выключение инициатора ∥ подтверждение его запроса: выключение уже держит учётку инициатора, когда
// приходит подтверждение; исполнитель ждёт её замок и видит новую версию — failed, запрос не исполнен
// правами, которых у инициатора уже нет.
func TestDisableInitiatorWhileApproving(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	ap, err := e.app.Invite(ctx, e.as(x), staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "support"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	hold := e.holdAccount(x.ID) // «выключение в процессе»: замок учётки X
	disabled := make(chan error, 1)
	go func() {
		_, err := e.app.Disable(ctx, owner, x.ID, "other", "", x.Email, metaWeb)
		disabled <- err
	}()
	e.waitLockWaiters(1)
	approved := make(chan error, 1)
	var res approvals.Approval
	go func() {
		var err error
		res, err = e.approve(b, ap.ID)
		approved <- err
	}()
	e.waitLockWaiters(2)
	hold()
	if err := <-disabled; err != nil {
		t.Fatalf("выключение: %v", err)
	}
	if err := <-approved; !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed {
		t.Fatalf("подтверждение: %+v %v", res, err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'n@wf.example'`); n != 0 {
		t.Fatal("учётка создана правами выключенного")
	}
}

// Встречные запросы (m7): I о T и T об I подтверждают одновременно разные сотрудники — без взаимной
// блокировки (40P01 → 500): одно исполняется, второе — failed (инициатор изменился). -count=20.
func TestCounterRequestsNoDeadlock(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	i := e.addMember("owner.i@wf.example", memberOpts{Role: "owner"})
	tm := e.addMember("owner.t@wf.example", memberOpts{Role: "owner"})
	c := e.as(e.addMember("owner.c@wf.example", memberOpts{Role: "owner"}))
	d := e.as(e.addMember("owner.d@wf.example", memberOpts{Role: "owner"}))
	r1, err := e.app.ChangeRole(ctx, e.as(i), tm.ID, perm.Admin, nil, metaWeb)
	if err != nil || r1 == nil {
		t.Fatal(err)
	}
	r2, err := e.app.ChangeRole(ctx, e.as(tm), i.ID, perm.Admin, nil, metaWeb)
	if err != nil || r2 == nil {
		t.Fatal(err)
	}
	var executed, stale atomic.Int32
	barrier(2, func(k int) {
		st, apID := c, r1.ID
		if k == 1 {
			st, apID = d, r2.ID
		}
		res, err := e.approve(st, apID)
		switch {
		case err == nil && res.Status == approvals.StatusExecuted:
			executed.Add(1)
		// второе: исполнитель увидел новую версию инициатора (failed) или запрос уже погашен (closed)
		case errors.Is(err, approvals.ErrStale) && res.Status == approvals.StatusFailed, errors.Is(err, approvals.ErrClosed):
			stale.Add(1)
		default:
			t.Errorf("исход %d: %+v %v", k, res, err)
		}
	})
	if executed.Load() != 1 || stale.Load() != 1 {
		t.Fatalf("исполнено %d, устарело %d", executed.Load(), stale.Load())
	}
}

// Подтверждающий перепроверяется в момент решения (m4): выключен или сменил роль после загрузки
// сессии — ошибка без записей, запрос остаётся pending (другой владелец подтвердит).
func TestApproverRechecked(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	bm := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	cm := e.addMember("owner.c@wf.example", memberOpts{Role: "owner"})
	b, c := e.as(bm), e.as(cm)
	ap, err := e.app.Invite(ctx, a, staff.InviteInput{Email: "n@wf.example", DisplayName: "Н", Role: "admin"}, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.app.OperatorDisable(ctx, bm.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := e.approve(b, ap.ID); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("выключенный подтверждающий: %v", err)
	}
	if st, _ := e.apState(ap.ID); st != approvals.StatusPending || e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'n@wf.example'`) != 0 {
		t.Fatalf("запрос %s", st)
	}
	// роль C сменилась (владелец → админ) после загрузки его сессии: права снимка устарели
	demote, err := e.app.ChangeRole(ctx, a, cm.ID, perm.Admin, nil, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	d := e.as(e.addMember("owner.d@wf.example", memberOpts{Role: "owner"}))
	e.mustApprove(d, demote.ID)
	_, err = e.approve(c, ap.ID)
	wantForbidden(t, err)
	if st, _ := e.apState(ap.ID); st != approvals.StatusPending {
		t.Fatalf("запрос %s", st)
	}
	// админ не подтверждает выдачу роли админа (право запроса grant_elevated)
	_, err = e.approve(e.as(e.addMember("admin@wf.example", memberOpts{Role: "admin"})), ap.ID)
	wantForbidden(t, err)
	e.mustApprove(d, ap.ID)
}

// Регистрация (10c): RegisterApprovals регистрирует четыре действия и проверку инициатора — Validate
// проходит, имена — по алфавиту.
func TestRegisterApprovals(t *testing.T) {
	e := newManageEnv(t)
	want := []string{"staff.access_reset", "staff.invite", "staff.revoke_owner", "staff.role_change"}
	got := e.reg.Actions()
	if len(got) != len(want) {
		t.Fatalf("действия %v", got)
	}
	for k := range want {
		if got[k] != want[k] {
			t.Fatalf("действия %v", got)
		}
	}
	if err := e.reg.Validate(); err != nil {
		t.Fatal(err)
	}
}
