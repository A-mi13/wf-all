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

// Хук идемпотентности исполняется в той же транзакции до fn: его запись живёт и умирает с ней.
func TestInTxRunsHookFirstInSameTx(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	if _, err := pool.Exec(ctx, "CREATE TABLE hook_probe (v int)"); err != nil {
		t.Fatal(err)
	}
	var order []string
	hook := func(ctx context.Context, tx pgx.Tx) (func(bool), error) {
		order = append(order, "hook")
		if got, ok := db.TxFrom(ctx); !ok || got != tx {
			t.Error("хук получил не ту транзакцию")
		}
		_, err := tx.Exec(ctx, "INSERT INTO hook_probe VALUES (1)")
		return nil, err
	}
	hctx := db.WithTxHook(ctx, hook)

	errBusiness := errors.New("бизнес-ошибка")
	err := db.InTx(hctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		order = append(order, "fn")
		return errBusiness
	})
	if !errors.Is(err, errBusiness) || len(order) != 2 || order[0] != "hook" {
		t.Fatalf("err=%v порядок=%v", err, order)
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM hook_probe").Scan(&n)
	if n != 0 {
		t.Fatal("запись хука пережила откат транзакции")
	}

	if err := db.InTx(hctx, pool, func(context.Context, pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM hook_probe").Scan(&n)
	if n != 1 {
		t.Fatalf("после коммита строк %d", n)
	}
}

func TestInTxHookErrorSkipsFn(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	errHook := errors.New("ключ занят")
	called := false
	err := db.InTx(db.WithTxHook(ctx, func(context.Context, pgx.Tx) (func(bool), error) { return nil, errHook }), pool,
		func(context.Context, pgx.Tx) error { called = true; return nil })
	if !errors.Is(err, errHook) || called {
		t.Fatalf("err=%v fn вызвана=%v", err, called)
	}
	noLeakedConns(t, pool)
}

// after хука сообщает исход транзакции: true — только после успешного Commit (запись уже видна
// другим соединениям), иначе false — ровно один вызов на транзакцию при любом исходе.
func TestInTxHookAfterReportsOutcome(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		hookErr error
		fn      func(ctx context.Context, tx pgx.Tx) error
		want    bool
		rows    int // строк tx_probe, видимых другим соединениям в момент after
	}{
		{name: "коммит", want: true, rows: 1, fn: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)")
			return err
		}},
		{name: "ошибка fn", fn: func(ctx context.Context, tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)")
			return boom
		}},
		{name: "ошибка хука", hookErr: boom, fn: func(context.Context, pgx.Tx) error {
			t.Error("fn вызвана после ошибки хука")
			return nil
		}},
		{name: "паника", fn: func(ctx context.Context, tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)")
			panic("boom")
		}},
		{name: "Goexit", fn: func(ctx context.Context, tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)")
			runtime.Goexit()
			return nil
		}},
		{name: "неудачный коммит", fn: func(ctx context.Context, tx pgx.Tx) error {
			// отложенная проверка уникальности падает только на COMMIT
			if _, err := tx.Exec(ctx, "INSERT INTO tx_probe VALUES (1)"); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO tx_deferred VALUES (1), (1)")
			return err
		}},
	}
	// одна база на все случаи: клон базы на подтест под нагрузкой стоит секунды
	ctx, pool, rows := probe(t)
	if _, err := pool.Exec(ctx, "CREATE TABLE tx_deferred (n int UNIQUE DEFERRABLE INITIALLY DEFERRED)"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, "TRUNCATE tx_probe, tx_deferred"); err != nil {
				t.Fatal(err)
			}
			var outcomes []bool
			visible := -1
			hook := func(context.Context, pgx.Tx) (func(bool), error) {
				return func(committed bool) {
					outcomes = append(outcomes, committed)
					visible = rows()
				}, tc.hookErr
			}
			done := make(chan error, 1)
			go func() {
				defer func() { _ = recover(); close(done) }() // паника из InTx — ожидаемый исход
				done <- db.InTx(db.WithTxHook(ctx, hook), pool, tc.fn)
			}()
			err := <-done
			if len(outcomes) != 1 || outcomes[0] != tc.want || visible != tc.rows {
				t.Fatalf("after: вызовы %v, строк видно %d; нужно [%v] и %d", outcomes, visible, tc.want, tc.rows)
			}
			if tc.want && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.name == "неудачный коммит" && err == nil {
				t.Fatal("Commit с нарушенной отложенной проверкой не вернул ошибку")
			}
			noLeakedConns(t, pool)
		})
	}
}
