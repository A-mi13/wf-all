package idempotency_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/idempotency/idempotencydb"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Чистка не удаляет ключ, который параллельно переиспользовала транзакция запроса (спека §6.4).
// Подзапрос DELETE видит снимок до коммита Claim: без условия срока на внешнем DELETE строка,
// уже продлённая на 24 ч, удалилась бы после снятия блокировки — и повтор исполнил бы действие
// второй раз.
func TestDeleteExpiredKeepsKeyReclaimedConcurrently(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash, created_at, expires_at)
		VALUES ($1, 'k', '/old', 'old', now() - interval '25 hours', now() - interval '1 hour')`, user); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := idempotencydb.New(tx).Claim(ctx, idempotencydb.ClaimParams{
		UserID: user, Key: "k", Endpoint: "/new", RequestHash: "new",
	}); err != nil {
		t.Fatalf("Claim просроченного ключа: %v", err)
	}

	// чистка на другом соединении упирается в блокировку строки, взятую Claim
	done := make(chan error, 1)
	go func() {
		_, err := idempotencydb.New(pool).DeleteExpired(ctx, 100)
		done <- err
	}()
	waitForLockWaiter(ctx, t, pool)

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DeleteExpired не завершился после коммита Claim")
	}

	var hash string
	if err := pool.QueryRow(ctx, `SELECT request_hash FROM idempotency_keys
		WHERE user_id = $1 AND key = 'k' AND expires_at > now()`, user).Scan(&hash); err != nil {
		t.Fatalf("переиспользованный ключ удалён чисткой: %v", err)
	}
	if hash != "new" {
		t.Fatalf("request_hash = %q, нужен new", hash)
	}
}

// waitForLockWaiter ждёт, пока в базе теста появится соединение, ожидающее блокировку.
func waitForLockWaiter(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("DeleteExpired не встал в ожидание блокировки строки")
}
