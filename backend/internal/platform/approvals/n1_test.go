package approvals_test

import (
	"context"
	"testing"
	"time"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/perm"
)

// n1 (ревью 8a раунда 1): у истёкшего запроса decided_at — его срок (expires_at), а не время тика
// ExpireDue или клика: список показывает истёкший pending со сроком, после тика и после клика время
// не «прыгает». Часы — на час позже срока.
func TestExpiredDecidedAtIsExpiresAt(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	byTick := e.request(a, actGrant, payload{V: 1})
	byApprove := e.request(a, actGrant, payload{V: 1})
	byReject := e.request(a, actGrant, payload{V: 1})
	byCancel := e.request(a, actGrant, payload{V: 1})
	e.clock.Set(byTick.ExpiresAt.Add(time.Hour))
	ctx := context.Background()
	if _, err := e.svc.Approve(ctx, b, "Борис", metaWeb, byApprove.ID, ""); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("approve просроченного: %v", err)
	}
	if _, err := e.svc.Reject(ctx, b, "Борис", metaWeb, byReject.ID, "нет"); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("reject просроченного: %v", err)
	}
	if _, err := e.svc.Cancel(ctx, a, "Инициатор", metaWeb, byCancel.ID); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("cancel просроченного: %v", err)
	}
	if n, err := e.svc.ExpireDue(ctx); err != nil || n != 1 {
		t.Fatalf("ExpireDue: n=%d err=%v", n, err)
	}
	for name, ap := range map[string]approvals.Approval{
		"тик": byTick, "approve": byApprove, "reject": byReject, "cancel": byCancel,
	} {
		r := e.row(ap.ID)
		if r.Status != approvals.StatusExpired || r.DecidedAt == nil || !r.DecidedAt.Equal(ap.ExpiresAt) {
			t.Errorf("%s: статус %s, decided_at %v, ждали %v", name, r.Status, r.DecidedAt, ap.ExpiresAt)
		}
	}
	// список после тика — то же время, что и до него
	items := must(e.list(b, approvals.BoxIncoming, approvals.StatusExpired, "", 10))
	if len(items) != 4 {
		t.Fatalf("истёкших в списке %d", len(items))
	}
	for _, it := range items {
		if it.DecidedAt == nil || !it.DecidedAt.Equal(it.ExpiresAt) {
			t.Errorf("в списке %s: decided_at %v, срок %v", it.ID, it.DecidedAt, it.ExpiresAt)
		}
	}
}
