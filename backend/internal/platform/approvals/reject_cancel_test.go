package approvals_test

import (
	"context"
	"strings"
	"testing"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/perm"
)

// Отклонение: комментарий обязателен (400 body.comment до транзакции); rejected с комментарием,
// исполнителя и проверки инициатора нет (выключенный инициатор не мешает отклонить).
func TestReject(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	for _, c := range []string{"", "   ", "\n\t"} {
		_, err := e.svc.Reject(context.Background(), b, "Борис", ap.ID, c)
		assertField(t, err, "body.comment", "required")
	}
	_, err := e.svc.Reject(context.Background(), b, "Борис", ap.ID, strings.Repeat("ж", 501))
	assertField(t, err, "body.comment", "maxLength")
	if e.row(ap.ID).Status != "pending" {
		t.Fatal("отказ проверки изменил запрос")
	}
	e.disable(a.StaffID)
	res, err := e.svc.Reject(context.Background(), b, "Борис", ap.ID, " рано ")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != approvals.StatusRejected || res.Comment != "рано" || res.DecidedBy != b.StaffID {
		t.Fatalf("ответ: %+v", res)
	}
	if r := e.row(ap.ID); r.Status != "rejected" || r.Comment != "рано" || r.Label != "Борис" || r.Error != "" {
		t.Fatalf("строка: %+v", r)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_rejected' AND object_id = $1
		AND actor_staff_id = $2 AND reason = 'рано'`, ap.ID, b.StaffID); n != 1 || e.decided(ap.ID) != 1 {
		t.Fatalf("аудит %d, событий %d", n, e.decided(ap.ID))
	}
	if e.execs.Load() != 0 {
		t.Fatal("отклонение вызвало исполнителя")
	}
}

// Отмена — только инициатор и только pending: чужой запрос (в том числе о себе) — ErrNotFound
// (контракт cancelApproval: «нет или он не ваш»), решённый — ErrClosed.
func TestCancel(t *testing.T) {
	e := newEnv(t)
	a, b, tgt := e.member(perm.Owner), e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1, Target: &tgt.StaffID})
	if _, err := e.svc.Cancel(context.Background(), b, "Борис", ap.ID); !isErr(err, approvals.ErrNotFound) {
		t.Fatalf("чужой: %v", err)
	}
	if _, err := e.svc.Cancel(context.Background(), tgt, "Цель", ap.ID); !isErr(err, approvals.ErrNotFound) {
		t.Fatalf("цель: %v", err)
	}
	if e.row(ap.ID).Status != "pending" {
		t.Fatal("чужая отмена изменила запрос")
	}
	reauth := *a
	reauth.Reauth = true
	if _, err := e.svc.Cancel(context.Background(), &reauth, "Анна", ap.ID); !isErr(err, approvals.ErrForbidden) {
		t.Fatalf("под reauth: %v", err)
	}
	res, err := e.svc.Cancel(context.Background(), a, "Анна", ap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != approvals.StatusCancelled || res.DecidedBy != a.StaffID || res.Error != "" {
		t.Fatalf("ответ: %+v", res)
	}
	if r := e.row(ap.ID); r.Status != "cancelled" || r.Label != "Анна" || *r.DecidedBy != a.StaffID || r.Error != "" {
		t.Fatalf("строка: %+v", r)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_cancelled' AND object_id = $1
		AND actor_staff_id = $2`, ap.ID, a.StaffID) != 1 || e.decided(ap.ID) != 1 {
		t.Fatal("нет аудита или события отмены")
	}
	if _, err := e.svc.Cancel(context.Background(), a, "Анна", ap.ID); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("повторная отмена: %v", err)
	}
}
