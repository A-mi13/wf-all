package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"

	"wf/backend/internal/platform/grants"
)

// Общий клон на процесс тестов пакета (спека сотрудников §6.3 п.8): тесты сценариев чтения без HTTP-конвейера
// не клонируют базу на каждый тест, а читают один засеянный клон под ролью прода. Запись в него запрещена
// (default_transaction_read_only), пул владельца тестам не отдаётся: тест, которому нужны свои строки или запись,
// берёт New*. Клон создаёт pgtestdb из того же шаблона, удаляет — Main после всех тестов пакета.

// SeedFunc — посев общего клона пулом владельца (суперпользователь): только данные, нужные чтениям пакета.
type SeedFunc = func(ctx context.Context, owner *pgxpool.Pool) error

// sharedEntry — общий клон одной роли.
type sharedEntry struct {
	once  sync.Once
	seed  uintptr // код функции посева: второй посев для роли — ошибка теста
	pools Pools
	err   error
}

var (
	// mainActive — пакет запущен через Main: есть кому удалить общие клоны.
	mainActive atomic.Bool

	sharedMu sync.Mutex
	shared   = map[string]*sharedEntry{}
	// sharedHost — «тест» pgtestdb для общих клонов: их уборка — в Main, а не в Cleanup первого теста.
	sharedHost = &processTB{}
)

// closureName — имя кода замыкания (`pkg.F.func1`, вложенного `pkg.F.func1.2`, литерала пакетной переменной
// `pkg.init.func1`/`pkg.glob..func1`) или значения метода (`pkg.T.M-fm`): их код общий у всех экземпляров при разных
// захваченных данных, поэтому посевом SharedAs они быть не могут (m5-5 ревью G2).
var closureName = regexp.MustCompile(`\.func\d+(\.\d+)*$|-fm$`)

// funcName — имя функции по адресу кода ("" — nil).
func funcName(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	return runtime.FuncForPC(ptr).Name()
}

// SharedAs — общий клон для чтений под ролью role (api, admin, worker): первый вызов для роли клонирует шаблон,
// применяет grants.sql, вызывает seed пулом владельца и отдаёт Pools{As} — пул под ролью с
// default_transaction_read_only = on; Owner = nil (владелец — только для посева). Следующие вызовы отдают тот
// же пул без посева. Ошибка посева или клонирования — Fatalf в каждом вызове для этой роли. Другой seed для той
// же роли — паника: два набора данных в одном клоне. seed — функция верхнего уровня (замыкание и значение метода —
// Fatalf): «тот же посев» узнаётся по коду функции. Пакет обязан объявить
// func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }.
func SharedAs(tb testing.TB, role string, seed SeedFunc) Pools {
	tb.Helper()
	if !mainActive.Load() {
		tb.Fatalf("dbtest.SharedAs: нет TestMain с dbtest.Main — общий клон некому удалить; " +
			"объяви func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }")
		return Pools{}
	}
	ptr := reflect.ValueOf(seed).Pointer()
	if name := funcName(ptr); seed == nil || closureName.MatchString(name) {
		tb.Fatalf("dbtest.SharedAs(%s): посев %q — не функция верхнего уровня: замыкание или значение метода с разными "+
			"данными делят код, и «тот же посев» не различить; объяви посев func seedX(ctx, owner) error", role, name)
		return Pools{}
	}
	sharedMu.Lock()
	e, ok := shared[role]
	if !ok {
		e = &sharedEntry{seed: ptr}
		shared[role] = e
	}
	sharedMu.Unlock()
	if e.seed != ptr {
		panic(fmt.Sprintf("dbtest.SharedAs: для роли %s уже засеян общий клон другой функцией посева", role))
	}
	e.once.Do(func() { e.pools, e.err = newShared(role, seed) })
	if e.err != nil {
		tb.Fatalf("dbtest.SharedAs(%s): %v", role, e.err)
		return Pools{}
	}
	return e.pools
}

// newShared — клон шаблона, права, посев, пул под ролью только на чтение. Fatalf pgtestdb перехватывается
// processTB и возвращается ошибкой.
func newShared(role string, seed SeedFunc) (_ Pools, err error) {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(processFatal)
			if !ok {
				panic(r)
			}
			err = f
		}
	}()
	c := Config()
	if err := dial(c); err != nil {
		return Pools{}, err
	}
	url := pgtestdb.Custom(sharedHost, c, migrator()).URL()
	ctx := context.Background()
	owner, err := pgxpool.NewWithConfig(ctx, mustPoolConfig(url))
	if err != nil {
		return Pools{}, fmt.Errorf("pgxpool: %w", err)
	}
	defer owner.Close() // владелец нужен только для прав и посева
	if err := applyGrants(ctx, url); err != nil {
		return Pools{}, err
	}
	if err := seed(ctx, owner); err != nil {
		return Pools{}, fmt.Errorf("посев: %w", err)
	}
	cfg := mustPoolConfig(url)
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	as, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return Pools{}, fmt.Errorf("pgxpool под ролью %s: %w", role, err)
	}
	sharedHost.Cleanup(as.Close) // уборка в обратном порядке: пул закрывается раньше, чем pgtestdb удалит базу
	return Pools{As: as}, nil
}

func mustPoolConfig(url string) *pgxpool.Config {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		panic(processFatal{fmt.Errorf("pgxpool: %w", err)})
	}
	cfg.MaxConns = PoolMaxConns
	return cfg
}

func applyGrants(ctx context.Context, url string) error {
	sqldb, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	return grants.Apply(ctx, sqldb)
}

// Main — запуск тестов пакета, где есть SharedAs: m.Run, затем уборка общих клонов (пулы, DROP DATABASE) при любом
// исходе тестов (клоны New* упавших тестов pgtestdb оставляет для разбора, общий — нет). Ошибка уборки — код выхода 1.
func Main(m *testing.M) int {
	mainActive.Store(true)
	return finishShared(sharedHost, m.Run())
}

// finishShared — уборка общих клонов host после m.Run с кодом code — при ЛЮБОМ исходе (m5-2 ревью G2): общий клон
// засеян посевом и только на чтение, разбирать в нём нечего, а оставленный копит базы. Ошибка уборки — код 1.
// Таймаут -timeout и паника теста роняют процесс до возврата m.Run — туда уборка не дотянется (брошенный клон
// убирает уборка локальной базы).
func finishShared(host *processTB, code int) int {
	if err := host.finish(); err != nil {
		fmt.Fprintln(os.Stderr, "dbtest: уборка общих клонов:", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// processFatal — Fatalf pgtestdb внутри processTB: паника с ошибкой, её ловит newShared или finish.
type processFatal struct{ error }

// processTB — pgtestdb.TB на весь процесс тестов: Cleanup копит уборку до Main, Fatalf — паника processFatal,
// журнал не пишется (pgtestdb логирует строку подключения). Failed всегда false: общий клон удаляется и после
// упавших тестов.
type processTB struct {
	mu       sync.Mutex
	cleanups []func()
}

func (p *processTB) Cleanup(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cleanups = append(p.cleanups, f)
}

func (*processTB) Failed() bool { return false }

func (*processTB) Fatalf(format string, args ...any) {
	panic(processFatal{fmt.Errorf(format, args...)})
}
func (*processTB) Helper()             {}
func (*processTB) Logf(string, ...any) {}

// finish — уборка в обратном порядке регистрации; первая ошибка возвращается, остальная уборка всё равно идёт.
func (p *processTB) finish() error {
	p.mu.Lock()
	cleanups := slices.Clone(p.cleanups)
	p.cleanups = nil
	p.mu.Unlock()
	var first error
	for _, f := range slices.Backward(cleanups) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					pf, ok := r.(processFatal)
					if !ok {
						panic(r)
					}
					if first == nil {
						first = pf
					}
				}
			}()
			f()
		}()
	}
	return first
}
