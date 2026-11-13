package httpapi_test

import (
	"testing"

	identityhttp "wf/backend/internal/identity/httpapi"
	"wf/backend/internal/platform/testkit/apitest"
)

// Нулевой Handler — каркас модуля identity (план I-1): каждая операция тега identity публичного контракта отвечает
// 501 http.not_implemented без паники, пока этап спеки её не реализует. Операции появляются с контрактом (Task 2–4:
// public). Собранный public.Server — TestNoStubOperations (internal/httpapi/public, список pendingOps).
func TestZeroHandlerNotImplemented(t *testing.T) {
	t.Parallel()
	n, bad := apitest.NotImplemented(identityhttp.Handler{})
	// Task 2 — 24 операции входа, аккаунта и сессий; Task 3–4 добавят свои
	if n != 24 {
		t.Fatalf("операций identity %d, ждали 24", n)
	}
	for _, m := range bad {
		t.Errorf("%s: не 501 http.not_implemented", m)
	}
}
