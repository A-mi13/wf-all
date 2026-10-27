package admin_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/testkit/apitest"
)

// Волна 2, HTTP Minor 1: сессию решившего заменили (revoked_reason = replaced: второй фактор, unlock или ack
// в другой вкладке) между конвейером и подписью решившего (Label). Ответ — 401 auth.unauthenticated БЕЗ
// стирания cookie: в браузере уже новая, стирание выкинуло бы сотрудника (в отличие от погашенной сессии —
// TestDecisionRevokedSessionClearsCookie). Запрос остаётся pending, решений в журнале нет. approve, reject и
// cancel.
func TestDecisionReplacedSessionKeepsCookie(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	id := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	for _, c := range []struct {
		verb string
		who  member
		body string
	}{
		{"approve", b, ""},
		{"reject", b, `{"comment":"Рано"}`},
		{"cancel", a, ""},
	} {
		tok := e.signIn(c.who) // заменённая сессия прошлого шага через минуту погаснет — каждому глаголу своя
		e.revokeOnLoad("replaced")
		rec := e.decide(c.verb, id, tok, "", c.body)
		wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
		noCookie(t, rec)
		if st, _ := e.approvalRow(id); st != approvals.StatusPending {
			t.Fatalf("%s: статус %s", c.verb, st)
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action LIKE 'platform.approval\_%' AND action <> 'platform.approval_requested'`); n != 0 {
		t.Fatalf("решений в журнале: %d", n)
	}
}

// То же из-под замка исполнителя approve: подпись решившего прошла (сессия жива), сессию заменили до замка
// учёток (execLocks → checkActor → auth.ErrSessionReplaced). Ответ — 401 без стирания cookie, транзакция
// решения откатилась целиком: запрос pending, решений в журнале нет. Шов — как у
// TestApproveSessionRevokedUnderLockClearsCookie: первый ManageShareAccount заменяет сессию с другого соединения.
func TestApproveSessionReplacedUnderLockKeepsCookie(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	id := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))

	var session atomic.Pointer[uuid.UUID]
	load := func(s *auth.Staff) {
		if s.StaffID == b.id {
			sid := s.SessionID
			session.Store(&sid)
		}
	}
	e.onLoad.Store(&load)
	var replaced atomic.Bool
	hook := func(sql string) {
		sid := session.Load()
		if sid == nil || !strings.Contains(sql, "-- name: ManageShareAccount ") || !replaced.CompareAndSwap(false, true) {
			return
		}
		e.exec(`UPDATE staff_sessions SET revoked_at = $1, revoked_reason = 'replaced' WHERE id = $2`, e.clock.Now(), *sid)
	}
	e.commits.onQuery.Store(&hook)
	rec := e.decide("approve", id, b.token, uuid.NewString(), "")
	e.commits.onQuery.Store(nil)
	e.onLoad.Store(nil)

	if !replaced.Load() {
		t.Fatal("шов не сработал: замка учёток исполнителя не было")
	}
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	noCookie(t, rec)
	if st, _ := e.approvalRow(id); st != approvals.StatusPending {
		t.Fatalf("статус %s, ждали pending (откат всего)", st)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action LIKE 'platform.approval\_%' AND action <> 'platform.approval_requested'`); n != 0 {
		t.Fatalf("решений в журнале: %d", n)
	}
}
