package admin_test

import (
	"testing"

	identityadmin "wf/backend/internal/identity/admin"
	"wf/backend/internal/platform/testkit/apitest"
)

// Нулевой Handler — каркас админских ручек identity (план I-1): каждая операция тега identity контракта admin
// отвечает 501 http.not_implemented без паники, пока этап спеки её не реализует. Операции появляются с контрактом
// (Task 4). Собранный admin.Server — TestNoStubOperations (internal/httpapi/admin, список pendingOps).
func TestZeroHandlerNotImplemented(t *testing.T) {
	t.Parallel()
	_, bad := apitest.NotImplemented(identityadmin.Handler{})
	for _, m := range bad {
		t.Errorf("%s: не 501 http.not_implemented", m)
	}
}
