package admin_test

import (
	"testing"

	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/platform/testkit/apitest"
)

// Нулевой Handler (&geoadmin.Handler{} — тесты конвейера; cmd/admin-api собирает New): listGeoCities отвечает 501
// http.not_implemented без паники. Собранный (New) — сквозные тесты httpapi/admin (geo_test.go).
func TestZeroHandlerNotImplemented(t *testing.T) {
	n, bad := apitest.NotImplemented(&geoadmin.Handler{})
	if n != 1 {
		t.Fatalf("операций geo %d, ждали 1", n)
	}
	for _, m := range bad {
		t.Errorf("%s: не 501 http.not_implemented", m)
	}
}
