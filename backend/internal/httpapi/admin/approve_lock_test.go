package admin_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
)

// Уборка №8 m3: сессию подтверждающего погасили между подписью решившего (Label — сессия ещё жива) и
// замком исполнителя (execLocks перепроверяет подтверждающего под замком учёток → auth.ErrSessionInvalid).
// Ответ — 401 со стиранием cookie, как у той же ошибки из подписи (TestDecisionRevokedSessionClearsCookie):
// approveProblem пропускает её к requestMeta, а не переводит сам в 401 без стирания. Транзакция решения
// откатилась целиком — запрос pending, решений в журнале нет. Шов — трассировщик пула сценариев: первый
// запрос ManageShareAccount (замок учёток исполнителя) гасит сессию с другого соединения.
func TestApproveSessionRevokedUnderLockClearsCookie(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	id := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))

	var session atomic.Pointer[uuid.UUID]
	load := func(s *auth.Staff) {
		if s.StaffID == b.id {
			sid := s.SessionID
			session.Store(&sid)
		}
	}
	e.onLoad.Store(&load)
	var revoked atomic.Bool
	hook := func(sql string) {
		sid := session.Load()
		if sid == nil || !strings.Contains(sql, "-- name: ManageShareAccount ") || !revoked.CompareAndSwap(false, true) {
			return
		}
		e.exec(`UPDATE staff_sessions SET revoked_at = $1, revoked_reason = 'logout' WHERE id = $2`, e.clock.Now(), *sid)
	}
	e.commits.onQuery.Store(&hook)
	rec := e.decide("approve", id, b.token, uuid.NewString(), "")
	e.commits.onQuery.Store(nil)
	e.onLoad.Store(nil)

	if !revoked.Load() {
		t.Fatal("шов не сработал: замка учёток исполнителя не было")
	}
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
	if st, _ := e.approvalRow(id); st != approvals.StatusPending {
		t.Fatalf("статус %s, ждали pending (откат всего)", st)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action LIKE 'platform.approval\_%' AND action <> 'platform.approval_requested'`); n != 0 {
		t.Fatalf("решений в журнале: %d", n)
	}
}
