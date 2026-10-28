package sqlmirror_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"wf/backend/internal/platform/testkit/sqlmirror"
)

// recorder — testing.TB, который копит ошибки стража вместо падения теста; Fatal — остановка через панику.
type recorder struct {
	testing.TB
	errs []string
}

type fatalStop struct{}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	panic(fatalStop{})
}

func (r *recorder) Fatal(args ...any) {
	r.errs = append(r.errs, fmt.Sprint(args...))
	panic(fatalStop{})
}

// check — ошибки стража на файле q.sql с текстом src.
func check(t *testing.T, src, table, staffTable string) []string {
	t.Helper()
	rec := &recorder{TB: t}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fatalStop); !ok {
					panic(r)
				}
			}
		}()
		sqlmirror.Check(rec, fstest.MapFS{"q.sql": {Data: []byte(src)}}, "q.sql", table, staffTable, 1)
	}()
	return rec.errs
}

const rateOrig = `-- name: DeleteExpired :execrows
-- комментарий оригинала
DELETE FROM rate_limits
WHERE tat < @now
  AND key IN (SELECT key FROM rate_limits WHERE tat < @now LIMIT @batch);
`

// Пара, равная после замены имени таблицы, проходит (комментарии и пробелы не считаются).
func TestCheckAcceptsMirror(t *testing.T) {
	src := rateOrig + `
-- name: StaffDeleteExpired :execrows
DELETE FROM staff_rate_limits
WHERE tat < @now   AND key IN (SELECT key FROM staff_rate_limits WHERE tat < @now LIMIT @batch);
`
	if errs := check(t, src, "rate_limits", "staff_rate_limits"); len(errs) != 0 {
		t.Fatalf("законная пара отвергнута: %v", errs)
	}
}

// Смешанный StaffX (m2 ревью G1): пишет таблицу сотрудников, а подзапросом читает таблицу игроков. Обратная
// замена staff → игроков склеивала его с оригиналом; прямая (оригинал с таблицей сотрудников) — нет.
func TestCheckRejectsMixedStaffQuery(t *testing.T) {
	for _, c := range []struct{ name, src, table, staffTable string }{
		{"rate_limits", rateOrig + `
-- name: StaffDeleteExpired :execrows
DELETE FROM staff_rate_limits
WHERE tat < @now
  AND key IN (SELECT key FROM rate_limits WHERE tat < @now LIMIT @batch);
`, "rate_limits", "staff_rate_limits"},
		{"idempotency_keys", `-- name: Get :one
SELECT status FROM idempotency_keys WHERE user_id = @user_id AND key = @key
  AND EXISTS (SELECT 1 FROM idempotency_keys WHERE user_id = @user_id);

-- name: StaffGet :one
SELECT status FROM staff_idempotency_keys WHERE user_id = @user_id AND key = @key
  AND EXISTS (SELECT 1 FROM idempotency_keys WHERE user_id = @user_id);
`, "idempotency_keys", "staff_idempotency_keys"},
	} {
		t.Run(c.name, func(t *testing.T) {
			errs := check(t, c.src, c.table, c.staffTable)
			if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "расходится") {
				t.Fatalf("смешанный Staff-запрос принят: %v", errs)
			}
		})
	}
}

// Оригинал, уже обращающийся к таблице сотрудников, — ошибка: прямая замена в нём неоднозначна.
func TestCheckRejectsOriginalWithStaffTable(t *testing.T) {
	src := `-- name: Count :one
SELECT count(*) FROM rate_limits, staff_rate_limits;

-- name: StaffCount :one
SELECT count(*) FROM staff_rate_limits, staff_rate_limits;
`
	errs := check(t, src, "rate_limits", "staff_rate_limits")
	if !strings.Contains(strings.Join(errs, "\n"), "Count обращается к staff_rate_limits") {
		t.Fatalf("ошибки %v, ждали отказ оригинала с таблицей сотрудников", errs)
	}
}
