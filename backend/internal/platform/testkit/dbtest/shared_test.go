package dbtest_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/testkit/dbtest"
)

// probeSeeds — сколько раз вызван probeSeed за процесс тестов пакета.
var probeSeeds atomic.Int32

// probeSeed — посев общего клона тестов SharedAs: таблица без прав у роли (проверка, что пул под ролью) и таблица
// с SELECT и INSERT у роли (проверка, что пул только на чтение: отказ — из-за read-only, а не из-за прав).
func probeSeed(ctx context.Context, owner *pgxpool.Pool) error {
	probeSeeds.Add(1)
	_, err := owner.Exec(ctx, `
		CREATE TABLE shared_probe_hidden (id int PRIMARY KEY);
		CREATE TABLE shared_probe_rw (id int PRIMARY KEY);
		GRANT SELECT, INSERT ON shared_probe_rw TO worker;
		INSERT INTO shared_probe_hidden VALUES (1);
		INSERT INTO shared_probe_rw VALUES (1);`)
	return err
}

func pgCode(err error) string {
	if pg, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pg.Code
	}
	return ""
}

// Пул As общего клона — под ролью прода, а не владельцем: таблица посева без прав у роли даёт 42501; владельца
// тесту не отдают (посев — единственное его назначение).
func TestSharedAsRole(t *testing.T) {
	ctx := context.Background()
	p := dbtest.SharedAs(t, "worker", probeSeed)
	if p.Owner != nil {
		t.Fatal("общий клон отдал пул владельца тесту")
	}
	var who string
	if err := p.As.QueryRow(ctx, "SELECT current_user").Scan(&who); err != nil || who != "worker" {
		t.Fatalf("current_user = %q (%v), нужен worker", who, err)
	}
	if _, err := p.As.Exec(ctx, "SELECT id FROM shared_probe_hidden"); pgCode(err) != "42501" {
		t.Fatalf("чтение таблицы без прав: %v, нужен отказ 42501", err)
	}
	var n int
	if err := p.As.QueryRow(ctx, "SELECT count(*) FROM shared_probe_rw").Scan(&n); err != nil || n != 1 {
		t.Fatalf("чтение посева: %d %v", n, err)
	}
}

// Общий клон — только на чтение: запись даже в таблицу с правом INSERT у роли отклоняется (25006), чтобы один
// тест не менял данные другим.
func TestSharedIsReadOnly(t *testing.T) {
	p := dbtest.SharedAs(t, "worker", probeSeed)
	if _, err := p.As.Exec(context.Background(), "INSERT INTO shared_probe_rw VALUES (2)"); pgCode(err) != "25006" {
		t.Fatalf("запись в общий клон: %v, нужен отказ 25006", err)
	}
}

// Посев — один раз на процесс и роль: повторные вызовы отдают тот же пул без второго посева.
func TestSharedSeedOnce(t *testing.T) {
	a := dbtest.SharedAs(t, "worker", probeSeed)
	b := dbtest.SharedAs(t, "worker", probeSeed)
	if got := probeSeeds.Load(); got != 1 {
		t.Fatalf("посевов: %d, ждали 1", got)
	}
	if a.As != b.As {
		t.Fatal("второй вызов отдал другой пул")
	}
}

// Другой посев для той же роли — ошибка теста (два набора данных в одном клоне): паника, а не тихий второй посев.
func TestSharedOtherSeedPanics(t *testing.T) {
	dbtest.SharedAs(t, "worker", probeSeed)
	defer func() {
		if recover() == nil {
			t.Fatal("другой посев для той же роли принят")
		}
	}()
	dbtest.SharedAs(t, "worker", otherSeed)
}

// otherSeed — посев, отличный от probeSeed (функция верхнего уровня: замыкания SharedAs отвергает).
func otherSeed(context.Context, *pgxpool.Pool) error { return nil }
