package db_test

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/testkit/dbtest"
)

func count(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) int {
	t.Helper()
	var n int
	if err := q.QueryRow(context.Background(), "SELECT count(*) FROM tx_probe").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func probe(t *testing.T) (context.Context, *pgxpool.Pool, func() int) {
	t.Helper()
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "CREATE TABLE tx_probe (n int)"); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, func() int { return count(t, pool) }
}

// noLeakedConns — соединение вернулось в пул: без отката/коммита оно осталось бы занятым,
// а строки при этом тоже не видны другим соединениям, так что одних rows() мало.
func noLeakedConns(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("занятых соединений = %d, нужно 0 — транзакция не завершена", n)
	}
}

func TestInTxCommitsAndExposesTx(t *testing.T) {
	ctx, pool, rows := probe(t)
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		got, ok := db.TxFrom(ctx)
		if !ok || got != tx {
			t.Fatal("TxFrom не вернул транзакцию InTx")
		}
		_, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)")
		return err
	})
	if err != nil || rows() != 1 {
		t.Fatalf("err = %v, строк = %d", err, rows())
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	ctx, pool, rows := probe(t)
	boom := errors.New("boom")
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
			return err
		}
		return boom
	})
	noLeakedConns(t, pool)
	if !errors.Is(err, boom) || rows() != 0 {
		t.Fatalf("err = %v, строк = %d", err, rows())
	}
}

func TestInTxRollsBackOnPanicAndRepanics(t *testing.T) {
	ctx, pool, rows := probe(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("паника проглочена")
			}
		}()
		_ = db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	noLeakedConns(t, pool)
	if rows() != 0 {
		t.Fatalf("строк = %d после паники", rows())
	}
}

// Спека §4.4: вложенный вызов не присоединяется к транзакции из ctx, а открывает свою —
// её откат не трогает внешнюю, и TxFrom внутри видит внутреннюю.
func TestNestedInTxOpensOwnTransaction(t *testing.T) {
	ctx, pool, rows := probe(t)
	errInner := errors.New("откатить внутреннюю")
	err := db.InTx(ctx, pool, func(ctx context.Context, outer pgx.Tx) error {
		if _, err := outer.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
			return err
		}
		innerErr := db.InTx(ctx, pool, func(ctx context.Context, inner pgx.Tx) error {
			if got, _ := db.TxFrom(ctx); got != inner || inner == outer {
				t.Fatal("вложенный InTx не открыл свою транзакцию")
			}
			_, _ = inner.Exec(ctx, "INSERT INTO tx_probe VALUES (2)")
			return errInner
		})
		if !errors.Is(innerErr, errInner) {
			t.Fatalf("внутренний InTx вернул %v, нужна ошибка колбэка", innerErr)
		}
		return nil
	})
	if err != nil || rows() != 1 {
		t.Fatalf("err = %v, строк = %d — внутренний откат задел внешнюю", err, rows())
	}
}

// Отменённый ctx не должен оставлять транзакцию висеть: откат идёт и после отмены.
func TestInTxRollsBackWhenContextCancelled(t *testing.T) {
	_, pool, rows := probe(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	noLeakedConns(t, pool)
	if !errors.Is(err, context.Canceled) || rows() != 0 {
		t.Fatalf("err = %v, строк = %d", err, rows())
	}
}

// runtime.Goexit в колбэке (t.FailNow в тесте) — не паника и не возврат: транзакцию всё равно
// нужно откатить, иначе соединение остаётся занятым навсегда.
func TestInTxRollsBackOnGoexit(t *testing.T) {
	ctx, pool, rows := probe(t)
	done := make(chan struct{})
	go func() {
		defer close(done) // Goexit завершает эту горутину, defer при этом отрабатывает
		_ = db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
				return err
			}
			runtime.Goexit()
			return nil
		})
	}()
	<-done
	noLeakedConns(t, pool)
	if rows() != 0 {
		t.Fatalf("строк = %d после Goexit", rows())
	}
}

func TestTxFromEmptyContext(t *testing.T) {
	if _, ok := db.TxFrom(context.Background()); ok {
		t.Fatal("в пустом ctx нашлась транзакция")
	}
}
