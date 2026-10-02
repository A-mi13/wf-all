package auth_test

import (
	"context"
	"errors"
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
	sid := uuid.New()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := l.LoadSession(context.Background(), sid); err != nil {
				t.Error(err)
			}
		})
	}
	time.Sleep(50 * time.Millisecond) // все десять встали в ожидание
	close(next.gate)
	wg.Wait()
	if n := next.calls.Load(); n != 1 {
		t.Fatalf("обращений %d, нужно 1", n)
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
