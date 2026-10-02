package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/testkit/clocktest"
)

// counting — загрузчик-заглушка: считает обращения, отвечает тем, что задал тест.
type counting struct {
	calls atomic.Int32
	mu    sync.Mutex
	err   error
	gate  chan struct{} // не nil — загрузка ждёт закрытия
}

func (c *counting) LoadSession(_ context.Context, sid uuid.UUID) (*auth.Principal, error) {
	c.calls.Add(1)
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return &auth.Principal{UserID: uuid.New(), SessionID: sid}, nil
}

// Review Focus 3: отозванная сессия перестаёт действовать не позже ttl.
func TestCachedLoaderTTL(t *testing.T) {
	c := clocktest.New(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	next := &counting{}
	l := auth.NewCachedLoader(next, 5*time.Second, c, 100)
	sid := uuid.New()
	ctx := context.Background()

	first, err := l.LoadSession(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(4 * time.Second)
	if again, _ := l.LoadSession(ctx, sid); again != first || next.calls.Load() != 1 {
		t.Fatalf("в пределах ttl — не из кэша: обращений %d", next.calls.Load())
	}

	// сессию отозвали: через ttl загрузчик спрашивается снова и отвечает отказом
	next.mu.Lock()
	next.err = auth.ErrSessionInvalid
	next.mu.Unlock()
	c.Advance(time.Second + time.Millisecond)
	if _, err := l.LoadSession(ctx, sid); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("после ttl отозванная сессия жива: %v", err)
	}
	// отказ не кэшируется: следующее обращение снова идёт в загрузчик
	_, _ = l.LoadSession(ctx, sid)
	if next.calls.Load() != 3 {
		t.Fatalf("обращений %d, нужно 3", next.calls.Load())
	}
}

// Срок записи считается от начала загрузки: прочитанное состояние сессии верно на момент
// запроса к базе, и долгая загрузка не продлевает жизнь отозванной сессии сверх ttl.
func TestCachedLoaderTTLFromLoadStart(t *testing.T) {
	c := clocktest.New(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	next := &counting{gate: make(chan struct{})}
	l := auth.NewCachedLoader(next, 5*time.Second, c, 100)
	sid := uuid.New()
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := l.LoadSession(ctx, sid)
		done <- err
	}()
	for next.calls.Load() == 0 { // загрузка началась: момент старта уже взят
		time.Sleep(time.Millisecond)
	}
	c.Advance(3 * time.Second) // база отвечала 3 с
	close(next.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	next.mu.Lock()
	next.err = auth.ErrSessionInvalid
	next.mu.Unlock()
	c.Advance(2*time.Second + time.Millisecond) // ttl от начала загрузки прошёл
	if _, err := l.LoadSession(ctx, sid); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("ttl отсчитан от конца загрузки — отозванная сессия жива: %v", err)
	}
}

// Десять параллельных запросов одной сессии — одно обращение к базе.
func TestCachedLoaderSingleflight(t *testing.T) {
	c := clocktest.New(time.Now())
	next := &counting{gate: make(chan struct{})}
	l := auth.NewCachedLoader(next, 5*time.Second, c, 100)
	open := sync.OnceFunc(func() { close(next.gate) })
	defer open()
	sid := uuid.New()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := l.LoadSession(context.Background(), sid); err != nil {
				t.Error(err)
			}
		})
	}
	waitWaiters(t, l, 10, open) // все десять встали в ожидание общей загрузки
	open()
	wg.Wait()
	if n := next.calls.Load(); n != 1 {
		t.Fatalf("обращений %d, нужно 1", n)
	}
}

// У загрузки свой крайний срок не дальше ttl по реальным часам — даже если у вызывающего
// срок длиннее: зависшая база не держит запросы и не раздаёт состояние старше ttl.
func TestCachedLoaderLoadDeadline(t *testing.T) {
	c := clocktest.New(time.Now())
	var deadline time.Time
	var has bool
	next := auth.SessionLoaderFunc(func(ctx context.Context, sid uuid.UUID) (*auth.Principal, error) {
		deadline, has = ctx.Deadline()
		return &auth.Principal{SessionID: sid}, nil
	})
	const ttl = 5 * time.Second
	l := auth.NewCachedLoader(next, ttl, c, 100)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if _, err := l.LoadSession(ctx, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if !has || deadline.After(time.Now().Add(ttl)) {
		t.Fatalf("у загрузки нет срока или он дальше ttl: %v, %v", has, deadline)
	}
}

// Загрузка дольше ttl — ошибка, а не Principal.
func TestCachedLoaderLoadTimeout(t *testing.T) {
	c := clocktest.New(time.Now())
	next := auth.SessionLoaderFunc(func(ctx context.Context, _ uuid.UUID) (*auth.Principal, error) {
		<-ctx.Done() // база зависла
		return nil, ctx.Err()
	})
	l := auth.NewCachedLoader(next, 20*time.Millisecond, c, 100)
	type result struct {
		p   *auth.Principal
		err error
	}
	done := make(chan result, 1)
	go func() {
		p, err := l.LoadSession(context.Background(), uuid.New())
		done <- result{p, err}
	}()
	select {
	case r := <-done:
		if !errors.Is(r.err, context.DeadlineExceeded) || r.p != nil {
			t.Fatalf("загрузка дольше ttl: %v, %v", r.p, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("у загрузки нет срока: зависшая база держит запрос")
	}
}

// Вызывающий, у которого истёк свой срок, уходит сразу; общая загрузка продолжается для
// остальных.
func TestCachedLoaderCallerCancel(t *testing.T) {
	c := clocktest.New(time.Now())
	next := &counting{gate: make(chan struct{})}
	open := sync.OnceFunc(func() { close(next.gate) })
	defer open()
	l := auth.NewCachedLoader(next, 5*time.Second, c, 100)
	sid := uuid.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gone := make(chan error, 1)
	go func() {
		_, err := l.LoadSession(ctx, sid)
		gone <- err
	}()
	stays := make(chan error, 1)
	go func() {
		_, err := l.LoadSession(context.Background(), sid)
		stays <- err
	}()
	waitWaiters(t, l, 2, open)

	cancel()
	select {
	case err := <-gone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("отменённый вызывающий: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("отменённый вызывающий ждёт загрузку")
	}

	open()
	if err := <-stays; err != nil {
		t.Fatalf("общая загрузка оборвалась: %v", err)
	}
	if n := next.calls.Load(); n != 1 {
		t.Fatalf("обращений %d, нужно 1", n)
	}
}

// Паника загрузчика идёт в чужой горутине singleflight: перехвачена и стала ошибкой, а не
// уронила процесс (без перехвата тестовый бинарь падает целиком).
func TestCachedLoaderPanic(t *testing.T) {
	c := clocktest.New(time.Now())
	next := auth.SessionLoaderFunc(func(context.Context, uuid.UUID) (*auth.Principal, error) {
		panic("загрузчик сломан")
	})
	l := auth.NewCachedLoader(next, 5*time.Second, c, 100)
	p, err := l.LoadSession(context.Background(), uuid.New())
	if err == nil || p != nil || !strings.Contains(err.Error(), "загрузчик сломан") {
		t.Fatalf("паника загрузчика: %v, %v", p, err)
	}
}

// ttl <= 0 или maxEntries <= 0 — ошибка конфигурации на старте: иначе срок загрузки 0
// кладёт всю аутентификацию.
func TestNewCachedLoaderRejectsBadConfig(t *testing.T) {
	c := clocktest.New(time.Now())
	for _, tc := range []struct {
		name       string
		ttl        time.Duration
		maxEntries int
	}{
		{"ttl 0", 0, 100},
		{"ttl < 0", -time.Second, 100},
		{"maxEntries 0", time.Second, 0},
		{"maxEntries < 0", time.Second, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("конфигурация принята")
				}
			}()
			auth.NewCachedLoader(auth.NoSessions, tc.ttl, c, tc.maxEntries)
		})
	}
}

// waitWaiters — ждёт n вызовов в общей загрузке не дольше 5 с; не дождался — открывает
// загрузку (чтобы горутины теста не повисли) и падает.
func waitWaiters(t *testing.T, l auth.SessionLoader, n int, open func()) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for auth.Waiters(l) < n {
		if time.Now().After(deadline) {
			open()
			t.Fatalf("ждущих общую загрузку %d, нужно %d", auth.Waiters(l), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Переполнение: кэш не растёт без предела.
func TestCachedLoaderBounded(t *testing.T) {
	c := clocktest.New(time.Now())
	next := &counting{}
	l := auth.NewCachedLoader(next, time.Minute, c, 3)
	ctx := context.Background()
	for range 10 {
		if _, err := l.LoadSession(ctx, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	if n := auth.CacheLen(l); n > 3 {
		t.Fatalf("в кэше %d записей при пределе 3", n)
	}
}

func TestNoSessions(t *testing.T) {
	if _, err := auth.NoSessions.LoadSession(context.Background(), uuid.New()); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("NoSessions: %v", err)
	}
}
