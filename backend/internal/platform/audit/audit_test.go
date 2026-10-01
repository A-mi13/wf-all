package audit_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func entry() audit.Entry {
	return audit.Entry{
		ActorUserID: id.New(), ActorRole: "city_moderator",
		Action: "identity.user_banned", ObjectType: "user", ObjectID: id.New(),
		Before: map[string]any{"status": "active", "email": "ivan@mail.ru"},
		After:  map[string]any{"status": "banned"},
		Reason: "spam", IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "test",
	}
}

func rows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteInTransactionMasksAndPersists(t *testing.T) {
	pool := dbtest.NewPool(t)
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		return audit.Write(ctx, entry())
	}); err != nil {
		t.Fatal(err)
	}
	var before, action, ip string
	if err := pool.QueryRow(context.Background(),
		"SELECT before::text, action, host(ip) FROM audit_log").Scan(&before, &action, &ip); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "ivan@mail.ru") || !strings.Contains(before, "i***@mail.ru") ||
		action != "identity.user_banned" || ip != "203.0.113.7" {
		t.Fatalf("запись аудита: before=%s action=%s ip=%s", before, action, ip)
	}
}

func TestWriteNeedsTransactionAndRollsBackWithIt(t *testing.T) {
	pool := dbtest.NewPool(t)
	if err := audit.Write(context.Background(), entry()); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("вне транзакции: %v", err)
	}
	_ = db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		if err := audit.Write(ctx, entry()); err != nil {
			t.Fatal(err)
		}
		return errors.New("действие не удалось")
	})
	if n := rows(t, pool); n != 0 {
		t.Fatalf("аудит пережил откат действия: %d", n)
	}
}

// Аудит чтения чувствительного (почта, связанные аккаунты) — без транзакции действия.
func TestWriteReadWithoutTransaction(t *testing.T) {
	pool := dbtest.NewPool(t)
	e := entry()
	e.Action, e.Before, e.After = "identity.email_viewed", nil, nil
	if err := audit.WriteRead(context.Background(), pool, e); err != nil {
		t.Fatal(err)
	}
	if n := rows(t, pool); n != 1 {
		t.Fatalf("строк %d", n)
	}
}

func TestWriteRejectsInvalidEntry(t *testing.T) {
	pool := dbtest.NewPool(t)
	for name, mutate := range map[string]func(*audit.Entry){
		"действие без модуля": func(e *audit.Entry) { e.Action = "banned" },
		"нет типа объекта":    func(e *audit.Entry) { e.ObjectType = "" },
	} {
		t.Run(name, func(t *testing.T) {
			e := entry()
			mutate(&e)
			if err := audit.WriteRead(context.Background(), pool, e); !errors.Is(err, audit.ErrInvalidEntry) {
				t.Fatalf("err = %v", err)
			}
			if n := rows(t, pool); n != 0 {
				t.Fatalf("невалидная запись попала в аудит: %d", n)
			}
		})
	}
}

// Системное действие (воркер, миграция): нулевые значения пишутся как NULL, а не как пустые строки.
func TestWriteSystemEntryStoresNulls(t *testing.T) {
	pool := dbtest.NewPool(t)
	e := audit.Entry{Action: "worker.cleanup_done", ObjectType: "session", ObjectID: uuid.Nil}
	if err := audit.WriteRead(context.Background(), pool, e); err != nil {
		t.Fatal(err)
	}
	var nulls bool
	if err := pool.QueryRow(context.Background(), `SELECT actor_user_id IS NULL AND actor_role IS NULL
		AND object_id IS NULL AND ip IS NULL AND user_agent IS NULL AND reason IS NULL
		AND before IS NULL AND after IS NULL FROM audit_log`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if !nulls {
		t.Fatal("нулевые значения записаны не как NULL")
	}
}
