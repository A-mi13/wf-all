package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func limiter(t *testing.T) (*ratelimit.PG, *clocktest.Fake, dbtest.Pools) {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api") // права прода: api считает лимиты
	c := clocktest.New(start)
	return ratelimit.NewPG(pools.As, c), c, pools
}

func TestAllowBurstThenRate(t *testing.T) {
	l, c, _ := limiter(t)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 60, Period: time.Minute, Burst: 3} // 1 в секунду, всплеск 3
	for i := range 3 {
		if r, err := l.Allow(ctx, "k", p); err != nil || !r.Allowed {
			t.Fatalf("запрос %d всплеска: %+v %v", i+1, r, err)
		}
	}
	r, err := l.Allow(ctx, "k", p)
	if err != nil || r.Allowed {
		t.Fatalf("четвёртый подряд прошёл: %+v %v", r, err)
	}
	if r.RetryAfter <= 0 || r.RetryAfter > time.Second {
		t.Fatalf("RetryAfter = %v, нужно (0, 1с]", r.RetryAfter)
	}
	c.Advance(time.Second)
	if r, _ := l.Allow(ctx, "k", p); !r.Allowed {
		t.Fatal("через интервал не прошёл")
	}
	// другой ключ — свой счёт
	if r, _ := l.Allow(ctx, "other", p); !r.Allowed {
		t.Fatal("чужой ключ ограничен")
	}
}

// Всплеск 1: первый запрос по свежему ключу проходит (окно = интервал), второй подряд — нет.
func TestAllowBurstOne(t *testing.T) {
	l, c, _ := limiter(t)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 6, Period: time.Minute, Burst: 1}
	if r, err := l.Allow(ctx, "k", p); err != nil || !r.Allowed {
		t.Fatalf("первый: %+v %v", r, err)
	}
	r, err := l.Allow(ctx, "k", p)
	if err != nil || r.Allowed {
		t.Fatalf("второй подряд прошёл: %+v %v", r, err)
	}
	if r.RetryAfter != 10*time.Second {
		t.Fatalf("RetryAfter = %v, нужно 10с", r.RetryAfter)
	}
	c.Advance(10 * time.Second)
	if r, _ := l.Allow(ctx, "k", p); !r.Allowed {
		t.Fatal("через интервал не прошёл")
	}
}

// Отказ не сдвигает tat: долбить в закрытую дверь не продлевает блокировку.
func TestDeniedDoesNotExtend(t *testing.T) {
	l, c, _ := limiter(t)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 1, Period: 10 * time.Second}
	_, _ = l.Allow(ctx, "k", p)
	for range 5 {
		_, _ = l.Allow(ctx, "k", p)
	}
	c.Advance(10 * time.Second)
	if r, _ := l.Allow(ctx, "k", p); !r.Allowed {
		t.Fatal("отказы продлили блокировку")
	}
}

// Гонка: 20 параллельных запросов при всплеске 5 — проходят ровно 5.
func TestAllowConcurrent(t *testing.T) {
	l, _, _ := limiter(t)
	p := ratelimit.Policy{Limit: 5, Period: time.Hour}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			r, err := l.Allow(context.Background(), "race", p)
			if err != nil {
				t.Error(err)
			}
			if r.Allowed {
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 5 {
		t.Fatalf("прошло %d, нужно 5", ok.Load())
	}
}

// Счётчик сигнала риска: Add расходует всегда, Over — порог без расхода.
func TestAddAndOver(t *testing.T) {
	l, c, _ := limiter(t)
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 5, Period: 15 * time.Minute}
	for i := range 5 {
		if over, _ := l.Over(ctx, "login:a", p); over {
			t.Fatalf("порог превышен после %d неудач", i)
		}
		if err := l.Add(ctx, "login:a", p); err != nil {
			t.Fatal(err)
		}
	}
	if over, _ := l.Over(ctx, "login:a", p); !over {
		t.Fatal("после 5 неудач порог не превышен")
	}
	c.Advance(3 * time.Minute) // одна неудача «выветрилась»
	if over, _ := l.Over(ctx, "login:a", p); over {
		t.Fatal("порог не отпускает со временем")
	}
	if over, _ := l.Over(ctx, "never", p); over {
		t.Fatal("ключ без истории превышен")
	}
}

func TestPolicyValidate(t *testing.T) {
	for _, p := range []ratelimit.Policy{
		{Limit: -1, Period: time.Second},
		{Limit: 1},
		{Limit: 1, Period: time.Second, Burst: -1},
		{Limit: 2, Period: time.Nanosecond},                  // интервал меньше наносекунды
		{Limit: 1, Period: 1000 * time.Hour, Burst: 1 << 40}, // окно переполняет time.Duration
		{Limit: 1, Period: -time.Second},                     // отрицательный период
		{Period: time.Second},                                // лимит 0 при заданном периоде
	} {
		if p.Validate() == nil {
			t.Fatalf("принята %+v", p)
		}
	}
	if (ratelimit.Policy{}).Validate() != nil {
		t.Fatal("нулевая политика (без лимита) отвергнута")
	}
	for _, p := range []ratelimit.Policy{
		{Limit: 1, Period: time.Second},
		{Limit: 1, Period: time.Second, Burst: 1},
		{Limit: 60, Period: time.Minute, Burst: 3},
	} {
		if err := p.Validate(); err != nil {
			t.Fatalf("отвергнута %+v: %v", p, err)
		}
	}
}

// Невалидная политика до базы не доходит: ошибка, а не случайный пропуск или блокировка.
func TestInvalidPolicyRejected(t *testing.T) {
	l := ratelimit.NewPG(nil, clocktest.New(start)) // база не нужна: проверка раньше
	ctx := context.Background()
	p := ratelimit.Policy{Limit: 1, Period: time.Second, Burst: -1}
	if _, err := l.Allow(ctx, "k", p); err == nil {
		t.Fatal("Allow принял невалидную политику")
	}
	if err := l.Add(ctx, "k", p); err == nil {
		t.Fatal("Add принял невалидную политику")
	}
	if _, err := l.Over(ctx, "k", p); err == nil {
		t.Fatal("Over принял невалидную политику")
	}
}
