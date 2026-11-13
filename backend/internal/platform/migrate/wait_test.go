package migrate_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/migrate"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

var t0 = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// guard — предел реального времени теста: ожидание, которое должно завершиться, но не завершилось.
const guard = 30 * time.Second

// logSink — журнал WaitSchema: тест ждёт строку (доказательство, что опрос идёт), а не спит.
type logSink struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	wake chan struct{}
}

func newLogSink() *logSink { return &logSink{wake: make(chan struct{}, 1)} }

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return n, err
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// logger — с уровнем DEBUG: каждый опрос пишет строку «всё ещё отстаёт», тест считает опросы по ней.
func (s *logSink) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(s, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// waitCount — строка с sub встретилась в журнале не меньше n раз.
func (s *logSink) waitCount(t *testing.T, sub string, n int) {
	t.Helper()
	deadline := time.After(guard)
	for strings.Count(s.String(), sub) < n {
		select {
		case <-s.wake:
		case <-deadline:
			t.Fatalf("в журнале меньше %d строк %q:\n%s", n, sub, s.String())
		}
	}
}

// waitLine — строка с sub появилась в журнале.
func (s *logSink) waitLine(t *testing.T, sub string) {
	t.Helper()
	deadline := time.After(guard)
	for !strings.Contains(s.String(), sub) {
		select {
		case <-s.wake:
		case <-deadline:
			t.Fatalf("в журнале нет %q:\n%s", sub, s.String())
		}
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func latest(t *testing.T) int64 {
	t.Helper()
	v, err := migrate.Latest()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// startWait — WaitSchema в горутине; канал — её итог.
func startWait(ctx context.Context, pool *pgxpool.Pool, want int64, timeout time.Duration, clk *clocktest.Fake, log *slog.Logger) <-chan error {
	done := make(chan error, 1)
	go func() { done <- migrate.WaitSchema(ctx, pool, want, timeout, time.Millisecond, clk, log) }()
	return done
}

func result(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(guard):
		t.Fatal("WaitSchema не вернулся")
		return nil
	}
}

func notDone(t *testing.T, done <-chan error, why string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s: WaitSchema вернулся раньше времени: %v", why, err)
	default:
	}
}

func behind(t *testing.T, err error, have, want int64, unknown bool) {
	t.Helper()
	var be *migrate.BehindError
	if !errors.Is(err, migrate.ErrSchemaBehind) || !errors.As(err, &be) {
		t.Fatalf("err = %v, ждали ErrSchemaBehind", err)
	}
	if be.Have != have || be.Want != want || be.Unknown != unknown {
		t.Fatalf("BehindError = %+v, ждали Have=%d Want=%d Unknown=%v", *be, have, want, unknown)
	}
}

// Latest — номер последней вшитой миграции: шаблон тестовой базы накатил их все, версия базы ему равна.
func TestLatest(t *testing.T) {
	t.Parallel()
	p, err := migrate.NewProvider(dbtest.New(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	dbv, err := p.GetDBVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := latest(t); got != dbv || got < 20 {
		t.Fatalf("Latest = %d, версия накатанной базы %d", got, dbv)
	}
}

// Ровно нужная версия — успех сразу (сравнение ≥, а не >).
func TestWaitSchemaReady(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	if err := result(t, startWait(t.Context(), pool, latest(t), 0, clocktest.New(t0), slog.New(slog.DiscardHandler))); err != nil {
		t.Fatalf("WaitSchema: %v", err)
	}
}

// База новее бинарника (старый инстанс при откате) — не ошибка.
func TestWaitSchemaNewer(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	exec(t, pool, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, latest(t)+1)
	if err := result(t, startWait(t.Context(), pool, latest(t), 0, clocktest.New(t0), slog.New(slog.DiscardHandler))); err != nil {
		t.Fatalf("WaitSchema: %v", err)
	}
}

func TestWaitSchemaBehind(t *testing.T) {
	t.Parallel()
	// версия поднялась во время ожидания — успех; до подъёма WaitSchema не возвращается
	t.Run("rises", func(t *testing.T) {
		t.Parallel()
		pool, want := dbtest.NewPool(t), latest(t)
		exec(t, pool, `DELETE FROM goose_db_version WHERE version_id = $1`, want)
		sink := newLogSink()
		done := startWait(t.Context(), pool, want, time.Hour, clocktest.New(t0), sink.logger())
		sink.waitLine(t, "схема базы отстаёт")
		notDone(t, done, "версия ещё не поднялась")
		exec(t, pool, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`, want)
		if err := result(t, done); err != nil {
			t.Fatalf("WaitSchema: %v", err)
		}
	})
	// часы ровно на границе таймаута: ошибка с текущей и нужной версией
	t.Run("timeout_exact", func(t *testing.T) {
		t.Parallel()
		pool, want := dbtest.NewPool(t), latest(t)
		exec(t, pool, `DELETE FROM goose_db_version WHERE version_id = $1`, want)
		clk, sink := clocktest.New(t0), newLogSink()
		done := startWait(t.Context(), pool, want, 10*time.Minute, clk, sink.logger())
		sink.waitLine(t, "схема базы отстаёт")
		clk.Advance(10*time.Minute - time.Nanosecond)
		notDone(t, done, "до границы таймаута")
		clk.Advance(time.Nanosecond)
		err := result(t, done)
		behind(t, err, want-1, want, false)
		if !strings.Contains(err.Error(), "wf-api") {
			t.Errorf("в ошибке нет подсказки оператору: %v", err)
		}
	})
	// пустая таблица версий (NULL в max) — версия 0; таймаут 0 — одна проверка
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		pool, want := dbtest.NewPool(t), latest(t)
		exec(t, pool, `DELETE FROM goose_db_version`)
		err := result(t, startWait(t.Context(), pool, want, 0, clocktest.New(t0), slog.New(slog.DiscardHandler)))
		behind(t, err, 0, want, false)
	})
}

// Нет права SELECT на goose_db_version (первый деплой с правкой grants.sql) — «схема отстаёт»: опрос с одной
// записью WARN; права выдали — успех; не выдали за таймаут — ErrSchemaBehind с неизвестной версией.
func TestWaitSchemaNoPrivilege(t *testing.T) {
	t.Parallel()
	t.Run("granted", func(t *testing.T) {
		t.Parallel()
		pools, want := dbtest.NewPoolsAs(t, "api"), latest(t)
		exec(t, pools.Owner, `REVOKE ALL ON goose_db_version FROM api`)
		sink := newLogSink()
		done := startWait(t.Context(), pools.As, want, time.Hour, clocktest.New(t0), sink.logger())
		sink.waitLine(t, "42501")
		sink.waitCount(t, "всё ещё отстаёт", 3) // несколько опросов без права — WARN не повторяется
		notDone(t, done, "права ещё не выданы")
		exec(t, pools.Owner, `GRANT SELECT ON goose_db_version TO api`)
		if err := result(t, done); err != nil {
			t.Fatalf("WaitSchema: %v", err)
		}
		if n := strings.Count(sink.String(), "level=WARN"); n != 1 {
			t.Fatalf("WARN в журнале %d раз, ждали один:\n%s", n, sink.String())
		}
	})
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		pools, want := dbtest.NewPoolsAs(t, "api"), latest(t)
		exec(t, pools.Owner, `REVOKE ALL ON goose_db_version FROM api`)
		clk, sink := clocktest.New(t0), newLogSink()
		done := startWait(t.Context(), pools.As, want, time.Minute, clk, sink.logger())
		sink.waitLine(t, "42501")
		clk.Advance(time.Minute)
		behind(t, result(t, done), 0, want, true)
	})
}

// Нет таблицы версий (пустая база, 42P01) — тоже «отстаёт»: появилась — успех.
func TestWaitSchemaNoTable(t *testing.T) {
	t.Parallel()
	pool, want := dbtest.NewPool(t), latest(t)
	exec(t, pool, `ALTER TABLE goose_db_version RENAME TO goose_db_version_off`)
	sink := newLogSink()
	done := startWait(t.Context(), pool, want, time.Hour, clocktest.New(t0), sink.logger())
	sink.waitLine(t, "42P01")
	notDone(t, done, "таблицы ещё нет")
	exec(t, pool, `ALTER TABLE goose_db_version_off RENAME TO goose_db_version`)
	if err := result(t, done); err != nil {
		t.Fatalf("WaitSchema: %v", err)
	}
}

// Прочая ошибка базы (недоступна) — ошибка сразу, без опроса до таймаута.
func TestWaitSchemaOtherError(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(context.Background(), "postgres://a@127.0.0.1:1/wf?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	err = result(t, startWait(t.Context(), pool, 1, time.Hour, clocktest.New(t0), slog.New(slog.DiscardHandler)))
	if err == nil || errors.Is(err, migrate.ErrSchemaBehind) {
		t.Fatalf("err = %v, ждали ошибку подключения", err)
	}
}

// SIGTERM во время ожидания — возврат сразу с отменой ctx.
func TestWaitSchemaCanceled(t *testing.T) {
	t.Parallel()
	pool, want := dbtest.NewPool(t), latest(t)
	exec(t, pool, `DELETE FROM goose_db_version WHERE version_id = $1`, want)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := newLogSink()
	done := startWait(ctx, pool, want, time.Hour, clocktest.New(t0), sink.logger())
	sink.waitLine(t, "схема базы отстаёт")
	cancel()
	if err := result(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, ждали context.Canceled", err)
	}
}
