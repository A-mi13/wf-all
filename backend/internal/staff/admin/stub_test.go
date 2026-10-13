package admin_test

import (
	"testing"

	"wf/backend/internal/platform/testkit/apitest"
	staffadmin "wf/backend/internal/staff/admin"
)

// Task 9a: контракт admin заморожен раньше сценариев — операции тега staff отвечают 501
// http.not_implemented, пока задачи 10a–10b не заменят заглушки. Все 27 операций — на месте.
func TestStubsNotImplemented(t *testing.T) {
	n, bad := apitest.NotImplemented(staffadmin.Handler{})
	if n != 27 {
		t.Fatalf("операций staff %d, ждали 27", n)
	}
	for _, m := range bad {
		t.Errorf("%s: не 501 http.not_implemented", m)
	}
}
