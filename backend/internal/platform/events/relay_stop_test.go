package events_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/testkit/dbtest"
)

// deafHandler — соединение не замечает отмену ctx, пока тест не отпустит release: так ведёт себя LISTEN, чей сервер
// пропал из сети (отмена не доходит, чтение висит). Отпущенный — обрывает чтение сроком, как обычный обработчик pgx.
type deafHandler struct {
	conn    net.Conn
	release <-chan struct{}
}

func (h *deafHandler) HandleCancel(context.Context) {
	go func() {
		<-h.release
		_ = h.conn.SetDeadline(time.Now())
	}()
}

func (h *deafHandler) HandleUnwatchAfterCancel() {}

// deafPool — пул, соединения которого (и отдельное LISTEN-соединение relay — копия его конфига) глухи к отмене.
func deafPool(t *testing.T, release <-chan struct{}) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dbtest.NewURL(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = dbtest.PoolMaxConns
	cfg.ConnConfig.BuildContextWatcherHandler = func(pc *pgconn.PgConn) ctxwatch.Handler {
		return &deafHandler{conn: pc.Conn(), release: release}
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// План C, R4: остановка relay ограничена сроком — зависший LISTEN не держит остановку воркера (иначе SIGKILL
// хостинга до мягкой остановки River). Run возвращает ErrRelayStopTimeout за срок; отпущенный LISTEN завершается.
func TestWorkerRelayStopBounded(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	pool := deafPool(t, release)
	t.Cleanup(free) // при падении — раньше закрытия пула и удаления базы (Cleanup — в обратном порядке)
	const stop = 300 * time.Millisecond
	r := events.NewRelay(pool, inserter(t, pool), twoSubscribers(t), discard,
		events.RelayConfig{Batch: 10, Poll: time.Hour, StopTimeout: stop})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	listeners := func() int {
		var n int
		_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE query = 'LISTEN `+events.Channel+`' AND datname = current_database()`).Scan(&n)
		return n
	}
	waitFor(t, "LISTEN relay", 10*time.Second, func() bool { return listeners() > 0 })
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, events.ErrRelayStopTimeout) {
			t.Fatalf("Run = %v, ждали ErrRelayStopTimeout", err)
		}
		if took := time.Since(start); took > stop+2*time.Second {
			t.Fatalf("остановка заняла %s при сроке %s", took, stop)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("relay с зависшим LISTEN не остановился за срок")
	}
	// отпущенный LISTEN закрывает своё соединение (иначе база теста не удалится)
	free()
	waitFor(t, "закрытие LISTEN после отпускания", 10*time.Second, func() bool { return listeners() == 0 })
}

// Обычная остановка (LISTEN слышит отмену) — без ошибки и без ожидания срока.
func TestRelayStopsCleanly(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	r := events.NewRelay(pool, inserter(t, pool), twoSubscribers(t), discard,
		events.RelayConfig{Batch: 10, Poll: time.Hour, StopTimeout: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("relay не остановился")
	}
}
