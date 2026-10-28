package idempotency_test

import (
	"os"
	"testing"

	"wf/backend/internal/platform/testkit/sqlmirror"
)

// Волна 2, HTTP непредусмотренный 2: ключи admin-api — staff_idempotency_keys, запросы — пары X/StaffX в
// queries/idempotency.sql (sqlc не параметризует имя таблицы). Пара обязана совпадать текстом после замены имени
// таблицы: правка Claim без StaffClaim (срок ключа, условие переиспользования) молча развела бы семантику
// идемпотентности public и admin. Каждый запрос над idempotency_keys — с парой, каждая пара — с оригиналом.
// Страж не вхолостую: пары Get, Claim, SaveResponse, DeleteExpired.
func TestStaffQueriesMirrorPublic(t *testing.T) {
	sqlmirror.Check(t, os.DirFS("queries"), "idempotency.sql", "idempotency_keys", "staff_idempotency_keys", 4)
}
