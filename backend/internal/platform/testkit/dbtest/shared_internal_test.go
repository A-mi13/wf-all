package dbtest

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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
