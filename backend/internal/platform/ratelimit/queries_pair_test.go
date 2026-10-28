package ratelimit_test

import (
	"os"
	"testing"

	"wf/backend/internal/platform/testkit/sqlmirror"
)

// План B, Task 3 (R2): лимиты admin-api — staff_rate_limits, запросы — пары X/StaffX в queries/ratelimit.sql.
// Пара обязана совпадать текстом после замены имени таблицы: правка Take без StaffTake (окно, условие) или
// DeleteExpired без StaffDeleteExpired (повторная проверка tat на внешнем DELETE) молча развела бы лимиты
// игроков и сотрудников. Страж не вхолостую: пары Take, Add, GetTAT, DeleteExpired.
func TestStaffQueriesMirrorPublic(t *testing.T) {
	sqlmirror.Check(t, os.DirFS("queries"), "ratelimit.sql", "rate_limits", "staff_rate_limits", 4)
}
