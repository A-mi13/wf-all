package admin_test

import (
	"testing"

	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/platform/testkit/apitest"
)

// Task 9a: listGeoCities отвечает 501 http.not_implemented, пока Task 10b не заменит заглушку.
func TestStubsNotImplemented(t *testing.T) {
	n, bad := apitest.NotImplemented(&geoadmin.Handler{})
	if n != 1 {
		t.Fatalf("операций geo %d, ждали 1", n)
	}
	for _, m := range bad {
		t.Errorf("%s: не 501 http.not_implemented", m)
	}
}
