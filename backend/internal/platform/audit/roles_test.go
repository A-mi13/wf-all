package audit

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"testing"

	"wf/backend/internal/platform/testkit/dbtest"
)

// literal — строковый литерал в выводе pg_get_constraintdef, например 'owner'::text; пустой
// литерал (второй аргумент coalesce) пропускается.
var literal = regexp.MustCompile(`'([^']*)'`)

// Списки ролей в checkActor — копия CHECK audit_log из 0020: страж ловит расхождение в обе стороны
// (роль убрали или добавили только в Go либо только в миграции).
func TestActorRolesMatchCheck(t *testing.T) {
	pool := dbtest.NewPool(t)
	union := maps.Clone(playerRoles)
	maps.Copy(union, staffRoles)
	maps.Copy(union, serviceRoles)
	for _, c := range []struct {
		constraint string
		want       map[string]bool
	}{
		{"audit_log_actor_role", union},           // все допустимые роли
		{"audit_log_staff_actor", staffRoles},     // роль автора-сотрудника
		{"audit_log_service_actor", serviceRoles}, // роль записи без автора
	} {
		var def string
		if err := pool.QueryRow(context.Background(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conrelid = 'audit_log'::regclass AND conname = $1`, c.constraint).Scan(&def); err != nil {
			t.Fatalf("%s: %v", c.constraint, err)
		}
		got := map[string]bool{}
		for _, m := range literal.FindAllStringSubmatch(def, -1) {
			if m[1] != "" {
				got[m[1]] = true
			}
		}
		if !maps.Equal(got, c.want) {
			t.Errorf("%s: в CHECK %v, в audit.go %v", c.constraint, slices.Sorted(maps.Keys(got)),
				slices.Sorted(maps.Keys(c.want)))
		}
	}
}
