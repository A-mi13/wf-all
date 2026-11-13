package admin_test

import (
	"testing"

	identityadmin "wf/backend/internal/identity/admin"
	"wf/backend/internal/platform/testkit/apitest"
)

// Нулевой Handler — каркас админских ручек identity (план I-1): каждая операция тега identity контракта admin
// отвечает 501 http.not_implemented без паники, пока этап спеки её не реализует. Собранный admin.Server —
// TestNoStubOperations (internal/httpapi/admin, список pendingOps).
func TestZeroHandlerNotImplemented(t *testing.T) {
	t.Parallel()
	n, bad := apitest.NotImplemented(identityadmin.Handler{})
	// Task 4 — 9 операций тега identity (спека §7.1); связь «сотрудник → игрок» — тег staff (staff/admin)
	if n != 9 {
		t.Fatalf("операций identity %d, ждали 9", n)
	}
	for _, m := range bad {
		t.Errorf("%s: не 501 http.not_implemented", m)
	}
}
