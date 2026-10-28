package dbtest

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
)

// Общие клоны пакета (SharedAs) удаляются после всех тестов — их освобождает Main.
func TestMain(m *testing.M) { os.Exit(Main(m)) }

// fatalOf — сообщение Fatalf из f (fatalRecorder), "" — f не упала.
func fatalOf(t *testing.T, f func(tb testing.TB)) string {
	t.Helper()
	rec := &fatalRecorder{TB: t}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fatalStop); !ok {
					panic(r)
				}
			}
		}()
		f(rec)
	}()
	return rec.msg
}

// failingSeeds — сколько раз вызван failingSeed за процесс тестов пакета: счётчик пакетный, как и общий клон роли
// (Once роли отрабатывает один раз на процесс, а не на итерацию -count).
var failingSeeds atomic.Int32

// failingSeed — посев, который всегда падает (функция верхнего уровня, как требует SharedAs).
func failingSeed(context.Context, *pgxpool.Pool) error {
	failingSeeds.Add(1)
	return errors.New("посев сломался")
}

// Ошибка посева — Fatal во всех вызовах SharedAs этой роли, посев не повторяется (второй тест не получает
// наполовину засеянный клон и не сеет его заново). Посев — ровно один за процесс: тест повторяем (-count=N).
func TestSharedSeedErrorFailsEveryCall(t *testing.T) {
	for i := range 2 {
		msg := fatalOf(t, func(tb testing.TB) { SharedAs(tb, "admin", failingSeed) })
		if !strings.Contains(msg, "посев сломался") {
			t.Fatalf("вызов %d: Fatalf %q, ждали ошибку посева", i+1, msg)
		}
	}
	if n := failingSeeds.Load(); n != 1 {
		t.Fatalf("посевов за процесс: %d, ждали 1", n)
	}
}

// Без TestMain с dbtest.Main клон некому удалить — SharedAs отказывает понятной ошибкой, а не копит базы.
func TestSharedNeedsMain(t *testing.T) {
	mainActive.Store(false)
	defer mainActive.Store(true)
	msg := fatalOf(t, func(tb testing.TB) {
		SharedAs(tb, "api", func(context.Context, *pgxpool.Pool) error { return nil })
	})
	if !strings.Contains(msg, "dbtest.Main") {
		t.Fatalf("Fatalf %q, ждали подсказку про dbtest.Main", msg)
	}
}

// Общий клон удаляется при любом исходе тестов пакета (m5-2 ревью G2): упавший тест не оставляет базу — она
// общая, засеяна посевом и для разбора не нужна (свои строки упавшего теста — в клоне New*, его pgtestdb
// оставляет). Код выхода — как у m.Run.
func TestFinishSharedDropsCloneOnFailure(t *testing.T) {
	c := Config()
	if err := dial(c); err != nil {
		t.Fatal(err)
	}
	host := &processTB{}
	clone := pgtestdb.Custom(host, c, migrator())
	t.Cleanup(func() { dropDatabase(t, c, clone.Database) }) // красный прогон не копит базы
	if !databaseExists(t, c, clone.Database) {
		t.Fatalf("клон %s не создан", clone.Database)
	}
	if code := finishShared(host, 1); code != 1 {
		t.Fatalf("код выхода %d, ждали 1 (как у m.Run)", code)
	}
	if databaseExists(t, c, clone.Database) {
		t.Fatalf("клон %s остался после упавшего пакета", clone.Database)
	}
}

// databaseExists — база name есть на сервере c.
func databaseExists(t *testing.T, c pgtestdb.Config, name string) bool {
	t.Helper()
	sqldb, err := c.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	var n int
	if err := sqldb.QueryRowContext(context.Background(), `SELECT count(*) FROM pg_database WHERE datname = $1`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// dropDatabase — база name удалена, если осталась.
func dropDatabase(t *testing.T, c pgtestdb.Config, name string) {
	t.Helper()
	sqldb, err := c.Connect()
	if err != nil {
		t.Error(err)
		return
	}
	defer sqldb.Close()
	if _, err := sqldb.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); err != nil {
		t.Error(err)
	}
}
