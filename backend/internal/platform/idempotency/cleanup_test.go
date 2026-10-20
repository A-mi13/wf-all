package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/testkit/dbtest"
)

// seedExpiry — в таблице name ключ old (просрочен) и live (живой час) одного субъекта.
func seedExpiry(t *testing.T, pools dbtest.Pools, name string) {
	t.Helper()
	if _, err := pools.Owner.Exec(context.Background(), `INSERT INTO `+name+` (user_id, key, endpoint, request_hash, expires_at) VALUES
		($1, 'old', '/x', 'h', now() - interval '1 second'), ($1, 'live', '/x', 'h', now() + interval '1 hour')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
}

// keysOf — ключи в таблице name.
func keysOf(t *testing.T, pools dbtest.Pools, name string) []string {
	t.Helper()
	var keys []string
	rows, err := pools.Owner.Query(context.Background(), "SELECT key FROM "+name+" ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return keys
}

// Периодическая чистка воркера (River, роль worker) — таблица игроков; ключи admin-api она не трогает.
func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	seedExpiry(t, pools, "idempotency_keys")
	seedExpiry(t, pools, "staff_idempotency_keys")
	if err := idempotency.NewCleanupWorker(pools.As).Work(context.Background(), &river.Job[idempotency.CleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	if keys := keysOf(t, pools, "idempotency_keys"); len(keys) != 1 || keys[0] != "live" {
		t.Fatalf("idempotency_keys: осталось %v", keys)
	}
	if keys := keysOf(t, pools, "staff_idempotency_keys"); len(keys) != 2 {
		t.Fatalf("staff_idempotency_keys: осталось %v — чистка воркера тронула ключи admin-api", keys)
	}
}

// DeleteExpired — чистка таблицы ключей процессом, у которого на неё права: ключи admin-api чистит
// admin-api (роль admin, тикер staff/jobs.Cleaner), у воркера прав на них нет. Порции по 1000: больше
// порции — всё равно удаляется всё просроченное; живые и чужая таблица остаются.
func TestDeleteExpiredStaffAsAdmin(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	ctx := context.Background()
	seedExpiry(t, pools, "staff_idempotency_keys")
	seedExpiry(t, pools, "idempotency_keys")
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO staff_idempotency_keys (user_id, key, endpoint, request_hash, expires_at)
		SELECT $1, 'bulk-' || g, '/x', 'h', now() - interval '1 minute' FROM generate_series(1, 1001) g`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	n, err := idempotency.DeleteExpired(ctx, pools.As, idempotency.Staff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1002 {
		t.Fatalf("удалено %d, ждали 1002", n)
	}
	if keys := keysOf(t, pools, "staff_idempotency_keys"); len(keys) != 1 || keys[0] != "live" {
		t.Fatalf("staff_idempotency_keys: осталось %v", keys)
	}
	if keys := keysOf(t, pools, "idempotency_keys"); len(keys) != 2 {
		t.Fatalf("idempotency_keys: осталось %v — чистка admin-api тронула ключи игроков", keys)
	}
}

// Под ролью worker чистка ключей admin-api — отказ в правах, а не тихий успех.
func TestDeleteExpiredStaffDeniedForWorker(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "worker")
	_, err := idempotency.DeleteExpired(context.Background(), pools.As, idempotency.Staff)
	if pg, ok := errors.AsType[*pgconn.PgError](err); !ok || pg.Code != "42501" {
		t.Fatalf("ждали 42501 (insufficient_privilege), получили %v", err)
	}
}
