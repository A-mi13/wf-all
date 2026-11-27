//go:build !wf_e2e

package main

import "testing"

// Ревью Task 11 m5, спека media §12a: прод-сборка admin-api (без тега wf_e2e) не содержит тестовых ручек — ни одного
// маршрута /_e2e/ на собранном хендлере (chi.Walk), маршруты контракта /v1/ при этом есть. Пара — TestAdminE2EBuildHasE2ERoutes.
func TestAdminProdBuildHasNoE2ERoutes(t *testing.T) {
	t.Parallel()
	if e2e, v1 := adminRoutes(t); e2e != 0 || v1 == 0 {
		t.Fatalf("маршрутов /_e2e: %d (ждали 0), /v1/: %d", e2e, v1)
	}
}
