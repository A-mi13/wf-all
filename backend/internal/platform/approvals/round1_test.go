package approvals_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
)

// I1 ревью: гашение пропустило запрос под замком решения, которое потом откатилось (approve из угнанной
// сессии → ErrSelf, чужой cancel → 404); инициатора сбросили и снова активировали с той же ролью —
// подтверждение не исполняет запрос: InitiatorCheck получает запрос целиком (payload с версией учётки
// инициатора) и отказывает — failed + ErrStale.
func TestSkippedCancelFailsAfterReactivation(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Admin), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1, InitVer: e.version(a.StaffID)})
	release := e.holdRow(ap.ID) // замок откатывающегося решения
	var n int
	if err := db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = e.svc.CancelByInitiator(ctx, tx, a.StaffID)
		e.disable(a.StaffID) // сброс доступа инициатора
		return err
	}); err != nil || n != 0 {
		t.Fatalf("гашение под замком: n=%d err=%v", n, err)
	}
	release()
	if e.row(ap.ID).Status != "pending" {
		t.Fatal("сценарий: запрос должен остаться pending")
	}
	e.reactivate(a.StaffID) // прошёл приглашение access_reset: снова active, та же роль
	res, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, "")
	if !isErr(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || res.Error != approvals.ErrorInitiatorChanged {
		t.Fatalf("после повторной активации: %v %+v", err, res)
	}
	if e.execs.Load() != 0 {
		t.Fatal("исполнитель вызван")
	}
	// контроль: без смены версии тот же путь исполняется
	ok := e.request(a, actGrant, payload{V: 1, InitVer: e.version(a.StaffID)})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ok.ID, ""); err != nil {
		t.Fatalf("неизменный инициатор: %v", err)
	}
}

// m5: проверка инициатора видит город запроса — модератор-инициатор scoped-действия своего города не
// получает failed.
func TestInitiatorCheckSeesCity(t *testing.T) {
	e := newEnv(t)
	city := id.New()
	mod, owner := e.member(perm.Moderator, city), e.member(perm.Owner)
	ap := e.request(mod, actRename, payload{V: 1, City: &city})
	res, err := e.svc.Approve(context.Background(), owner, "Ольга", metaWeb, ap.ID, "")
	if err != nil || res.Status != approvals.StatusExecuted {
		t.Fatalf("scoped-инициатор: %v %+v", err, res)
	}
}

// I2: IP и User-Agent решившего и инициатора — в аудите запроса и решений; у решений системы — пусто.
func TestAuditMeta(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	executed := e.request(a, actGrant, payload{V: 1})
	rejected := e.request(a, actGrant, payload{V: 1})
	cancelled := e.request(a, actGrant, payload{V: 1})
	failed := e.request(a, actGrant, payload{V: 1})
	expired := e.request(a, actGrant, payload{V: 1})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, executed.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reject(context.Background(), b, "Борис", metaWeb, rejected.ID, "нет"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Cancel(context.Background(), a, "Анна", metaWeb, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	e.setExec(func(context.Context, pgx.Tx, json.RawMessage, *auth.Staff) error { return approvals.Fail(errBiz) })
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, failed.ID, ""); !errors.Is(err, errBiz) {
		t.Fatal(err)
	}
	withMeta := `SELECT count(*) FROM audit_log WHERE object_id = $1 AND action = $2
		AND user_agent = 'Firefox/140 test' AND host(ip) = '203.0.113.7'`
	for apID, action := range map[uuid.UUID]string{
		executed.ID: "platform.approval_requested", rejected.ID: "platform.approval_rejected",
		cancelled.ID: "platform.approval_cancelled", failed.ID: "platform.approval_failed",
	} {
		if n := e.count(withMeta, apID, action); n != 1 {
			t.Errorf("%s: записей с IP и UA %d", action, n)
		}
	}
	if n := e.count(withMeta, executed.ID, "platform.approval_executed"); n != 1 {
		t.Errorf("executed: записей с IP и UA %d", n)
	}
	e.clock.Advance(approvals.TTL)
	if _, err := e.svc.ExpireDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE object_id = $1 AND action = 'platform.approval_expired'
		AND user_agent IS NULL AND ip IS NULL`, expired.ID); n != 1 {
		t.Fatalf("expired от системы: %d", n)
	}
}

// m1: ожидающий с вышедшим сроком (ExpireDue ещё не дошёл) — не во «Входящих · pending» и не в счётчике;
// в expired и без фильтра — со статусом expired, в обоих ящиках.
func TestListExpiredPending(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	e.clock.Set(ap.ExpiresAt.Add(-time.Microsecond))
	if got := ids(must(e.list(b, approvals.BoxIncoming, approvals.StatusPending, "", 10))); len(got) != 1 {
		t.Fatalf("за микросекунду до срока: %v", got)
	}
	e.clock.Set(ap.ExpiresAt)
	for _, box := range []struct {
		st  *auth.Staff
		box string
	}{{b, approvals.BoxIncoming}, {a, approvals.BoxMine}} {
		if got := must(e.list(box.st, box.box, approvals.StatusPending, "", 10)); len(got) != 0 {
			t.Fatalf("%s pending: %+v", box.box, got)
		}
		exp := must(e.list(box.st, box.box, approvals.StatusExpired, "", 10))
		all := must(e.list(box.st, box.box, "", "", 10))
		for _, items := range [][]approvals.Approval{exp, all} {
			if len(items) != 1 || items[0].Status != approvals.StatusExpired || items[0].DecidedAt == nil ||
				!items[0].DecidedAt.Equal(ap.ExpiresAt) || items[0].DecidedByLabel != approvals.SystemLabel {
				t.Fatalf("%s истёкший: %+v", box.box, items)
			}
		}
	}
	// после ExpireDue — то же самое
	if _, err := e.svc.ExpireDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := must(e.list(b, approvals.BoxIncoming, approvals.StatusExpired, "", 10)); len(got) != 1 {
		t.Fatalf("после ExpireDue: %+v", got)
	}
}

// m2: сотрудник без права запроса получает 403 раньше любых других ответов и ничего не меняет:
// просроченный запрос не переводится его кликом в expired, состояние решённого не раскрывается.
func TestForbiddenBeforeState(t *testing.T) {
	e := newEnv(t)
	owner, owner2, admin := e.member(perm.Owner), e.member(perm.Owner), e.member(perm.Admin)
	stale := e.request(owner, actGrant, payload{V: 1, Elevated: true})
	done := e.request(owner, actGrant, payload{V: 1, Elevated: true})
	if _, err := e.svc.Reject(context.Background(), owner2, "Вера", metaWeb, done.ID, "нет"); err != nil {
		t.Fatal(err)
	}
	about := e.request(owner, actGrant, payload{V: 1, Elevated: true, Target: &admin.StaffID})
	e.clock.Set(stale.ExpiresAt)
	for name, apID := range map[string]uuid.UUID{"просроченный": stale.ID, "решённый": done.ID, "о нём": about.ID} {
		if _, err := e.svc.Approve(context.Background(), admin, "Админ", metaWeb, apID, ""); !isErr(err, approvals.ErrForbidden) {
			t.Errorf("approve %s: %v", name, err)
		}
		if _, err := e.svc.Reject(context.Background(), admin, "Админ", metaWeb, apID, "нет"); !isErr(err, approvals.ErrForbidden) {
			t.Errorf("reject %s: %v", name, err)
		}
	}
	if r := e.row(stale.ID); r.Status != "pending" || e.audits(stale.ID, "platform.approval_expired") != 0 {
		t.Fatalf("клик без права изменил запрос: %+v", r)
	}
}

// m3: отказ исполнителя без кода клиента — в error и аудите общий execution_failed, не текст ошибки
// (в нём могут быть ПД, а error видят «Входящие»).
func TestFailedWithoutCode(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	errMail := errors.New("почта ivan@mail.ru занята")
	e.setExec(func(context.Context, pgx.Tx, json.RawMessage, *auth.Staff) error { return approvals.Fail(errMail) })
	res, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, "")
	if !errors.Is(err, errMail) || res.Error != approvals.ErrorExecutionFailed {
		t.Fatalf("%v %+v", err, res)
	}
	if r := e.row(ap.ID); r.Error != approvals.ErrorExecutionFailed {
		t.Fatalf("строка: %+v", r)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE object_id = $1 AND after::text LIKE '%ivan%'`, ap.ID); n != 0 {
		t.Fatalf("текст ошибки в аудите: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE object_id = $1 AND action = 'platform.approval_failed'
		AND after->>'error_code' = 'execution_failed'`, ap.ID); n != 1 {
		t.Fatalf("код в аудите: %d", n)
	}
}
