package ratelimit_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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

// allow — Allow без проглатывания ошибки.
func allow(t *testing.T, l ratelimit.Limiter, key string, p ratelimit.Policy) ratelimit.Result {
	t.Helper()
	r, err := l.Allow(context.Background(), key, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// over — Over без проглатывания ошибки.
func over(t *testing.T, l ratelimit.Limiter, key string, p ratelimit.Policy) bool {
	t.Helper()
	o, err := l.Over(context.Background(), key, p)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestAllowBurstThenRate(t *testing.T) {
	l, c, _ := limiter(t)
	p := ratelimit.Policy{Limit: 60, Period: time.Minute, Burst: 3} // 1 в секунду, всплеск 3
	for i := range 3 {
		if r := allow(t, l, "k", p); !r.Allowed {
			t.Fatalf("запрос %d всплеска: %+v", i+1, r)
		}
	}
	r := allow(t, l, "k", p)
	if r.Allowed {
		t.Fatalf("четвёртый подряд прошёл: %+v", r)
	}
	if r.RetryAfter <= 0 || r.RetryAfter > time.Second {
		t.Fatalf("RetryAfter = %v, нужно (0, 1с]", r.RetryAfter)
	}
	c.Advance(time.Second)
	if r := allow(t, l, "k", p); !r.Allowed {
		t.Fatal("через интервал не прошёл")
	}
	// другой ключ — свой счёт
	if r := allow(t, l, "other", p); !r.Allowed {
		t.Fatal("чужой ключ ограничен")
	}
}

// Всплеск 1: первый запрос по свежему ключу проходит (окно = интервал), второй подряд — нет.
func TestAllowBurstOne(t *testing.T) {
	l, c, _ := limiter(t)
	p := ratelimit.Policy{Limit: 6, Period: time.Minute, Burst: 1}
	if r := allow(t, l, "k", p); !r.Allowed {
		t.Fatalf("первый: %+v", r)
	}
	r := allow(t, l, "k", p)
	if r.Allowed {
		t.Fatalf("второй подряд прошёл: %+v", r)
	}
	if r.RetryAfter != 10*time.Second {
		t.Fatalf("RetryAfter = %v, нужно 10с", r.RetryAfter)
	}
	c.Advance(10 * time.Second)
	if r := allow(t, l, "k", p); !r.Allowed {
		t.Fatal("через интервал не прошёл")
	}
}

// Интервал не кратен микросекунде (60 с / 7): база округляет интервал и окно до мкс каждое
// отдельно — всплеск обязан пропустить ровно Limit запросов, не меньше.
func TestAllowIntervalNotMicrosecondMultiple(t *testing.T) {
	l, _, _ := limiter(t)
	for _, p := range []ratelimit.Policy{
		{Limit: 7, Period: time.Minute},
		{Limit: 9, Period: time.Minute},
		{Limit: 6, Period: time.Second},
	} {
		key := fmt.Sprintf("%d/%s", p.Limit, p.Period)
		for i := range p.Limit {
			if r := allow(t, l, key, p); !r.Allowed {
				t.Fatalf("%+v: запрос %d из %d не прошёл", p, i+1, p.Limit)
			}
		}
		if r := allow(t, l, key, p); r.Allowed {
			t.Fatalf("%+v: запрос сверх всплеска прошёл", p)
		}
	}
}

// Отказ не сдвигает tat: долбить в закрытую дверь не продлевает блокировку.
func TestDeniedDoesNotExtend(t *testing.T) {
	l, c, _ := limiter(t)
	p := ratelimit.Policy{Limit: 1, Period: 10 * time.Second}
	if r := allow(t, l, "k", p); !r.Allowed {
		t.Fatal("первый не прошёл")
	}
	for range 5 {
		if r := allow(t, l, "k", p); r.Allowed {
			t.Fatal("сверх лимита прошёл")
		}
	}
	c.Advance(10 * time.Second)
	if r := allow(t, l, "k", p); !r.Allowed {
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
		if over(t, l, "login:a", p) {
			t.Fatalf("порог превышен после %d неудач", i)
		}
		if err := l.Add(ctx, "login:a", p); err != nil {
			t.Fatal(err)
		}
	}
	if !over(t, l, "login:a", p) {
		t.Fatal("после 5 неудач порог не превышен")
	}
	c.Advance(3 * time.Minute) // одна неудача «выветрилась»
	if over(t, l, "login:a", p) {
		t.Fatal("порог не отпускает со временем")
	}
	if over(t, l, "never", p) {
		t.Fatal("ключ без истории превышен")
	}
}

func TestPolicyValidate(t *testing.T) {
	for _, p := range []ratelimit.Policy{
		{Limit: -1, Period: time.Second},
		{Limit: 1},
		{Limit: 1, Period: time.Second, Burst: -1},
		{Limit: 2, Period: time.Nanosecond},                  // интервал меньше наносекунды
		{Limit: 2, Period: time.Microsecond},                 // интервал 500 нс — меньше микросекунды
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
		{Limit: 1, Period: time.Microsecond}, // ровно микросекунда — можно
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

// noRows — база, где любой запрос строки отвечает «0 строк»: отказ Take, а строку между Take и
// GetTAT удалила чистка.
type noRows struct{}

func (noRows) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (noRows) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, pgx.ErrNoRows }
func (noRows) QueryRow(context.Context, string, ...any) pgx.Row        { return noRow{} }

type noRow struct{}

func (noRow) Scan(...any) error { return pgx.ErrNoRows }

// Строку удалили между отказом и чтением tat — отказ с повтором через интервал, не ошибка.
func TestAllowDeniedRowGone(t *testing.T) {
	l := ratelimit.NewPG(noRows{}, clocktest.New(start))
	r, err := l.Allow(context.Background(), "k", ratelimit.Policy{Limit: 6, Period: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if r.Allowed || r.RetryAfter != 10*time.Second {
		t.Fatalf("got %+v, нужно отказ с RetryAfter 10с", r)
	}
}
