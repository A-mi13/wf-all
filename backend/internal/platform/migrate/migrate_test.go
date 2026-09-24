// backend/internal/platform/migrate/migrate_test.go
package migrate_test

import (
	"context"
	"testing"

	"wf/backend/internal/platform/migrate"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Клон уже мигрирован шаблоном: откатываем всё до нуля и накатываем снова.
// Ловит сломанные Down-секции и несовместимость с текущей версией PostgreSQL.
func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	p, err := migrate.NewProvider(dbtest.New(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if v, _ := p.GetDBVersion(ctx); v != 0 {
		t.Fatalf("version after down = %d", v)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
