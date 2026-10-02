// Package ratelimit — ограничение частоты запросов (спека бэкенда §6.6): GCRA, одна строка
// rate_limits на ключ, один UPSERT на проверку вне бизнес-транзакции. Ключи IP, пользователь,
// устройство — в Middleware; почта — в app/ модуля (тело запроса middleware не видит).
// Подсеть — только сигнал риска (пакет risk), не жёсткий 429: за CGNAT тысячи людей.
//
// GCRA: интервал T = Period/Limit, окно T·Burst. Запрос проходит, если
// max(tat, now) + T − now ≤ T·Burst; тогда tat сдвигается на T. Over — следующий Allow не
// пройдёт: tat − now > T·(Burst−1).
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/ratelimit/ratelimitdb"
)

// Policy — Limit запросов за Period, подряд — до Burst (0 — равен Limit). Нулевая Policy
// (Limit 0) — ключ не ограничивается.
type Policy struct {
	Limit  int
	Period time.Duration
	Burst  int
}

// Validate — политика осмысленна. Всплеск у валидной политики ≥ 1, поэтому окно T·Burst ≥ T:
// свежая строка (INSERT в Take без проверки окна) никогда не пропускает лишнего.
func (p Policy) Validate() error {
	switch {
	case p == (Policy{}):
		return nil
	case p.Limit < 1:
		return fmt.Errorf("ratelimit: лимит %d — нужен ≥ 1", p.Limit)
	case p.Period <= 0:
		return fmt.Errorf("ratelimit: период %s — нужен > 0", p.Period)
	case p.Burst < 0:
		return fmt.Errorf("ratelimit: всплеск %d — нужен ≥ 0", p.Burst)
	case p.interval() <= 0:
		return fmt.Errorf("ratelimit: %d за %s — интервал меньше наносекунды", p.Limit, p.Period)
	case p.interval() > time.Duration(math.MaxInt64)/time.Duration(p.burst()):
		return fmt.Errorf("ratelimit: всплеск %d при интервале %s — окно не помещается в time.Duration", p.burst(), p.interval())
	}
	return nil
}

func (p Policy) off() bool { return p.Limit == 0 }

func (p Policy) interval() time.Duration { return p.Period / time.Duration(p.Limit) }

func (p Policy) burst() int {
	if p.Burst > 0 {
		return p.Burst
	}
	return p.Limit
}

func (p Policy) window() time.Duration { return p.interval() * time.Duration(p.burst()) }

// check — нулевая политика (не ограничивать) или ошибка невалидной: до базы невалидное не
// доходит — иначе отрицательный интервал молча пропускал бы всё или блокировал навсегда.
func (p Policy) check() (off bool, err error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	return p.off(), nil
}

type Result struct {
	Allowed    bool
	RetryAfter time.Duration // у отказа — когда следующий запрос пройдёт
}

// Limiter — за интерфейсом: переезд на Valkey — замена реализации.
type Limiter interface {
	// Allow — расходует, если запрос влезает в лимит.
	Allow(ctx context.Context, key string, p Policy) (Result, error)
	// Add — расходует всегда: счётчик сигнала риска (неудачный вход).
	Add(ctx context.Context, key string, p Policy) error
	// Over — порог превышен (следующий Allow не пройдёт), без расхода.
	Over(ctx context.Context, key string, p Policy) (bool, error)
}

// PG — Limiter на таблице rate_limits.
type PG struct {
	q     *ratelimitdb.Queries
	clock clock.Clock
}

func NewPG(db ratelimitdb.DBTX, c clock.Clock) *PG { return &PG{q: ratelimitdb.New(db), clock: c} }

func (l *PG) Allow(ctx context.Context, key string, p Policy) (Result, error) {
	if off, err := p.check(); err != nil || off {
		return Result{Allowed: off}, err
	}
	now := l.clock.Now()
	t, w := p.interval(), p.window()
	_, err := l.q.Take(ctx, ratelimitdb.TakeParams{Key: key, Now: now, IntervalS: t.Seconds(), WindowS: w.Seconds()})
	if err == nil {
		return Result{Allowed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}
	tat, err := l.q.GetTAT(ctx, key)
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: %w", err)
	}
	// следующий запрос пройдёт, когда max(tat, now) + T − now ≤ окно
	base := tat
	if base.Before(now) {
		base = now
	}
	retry := base.Sub(now) + t - w
	if retry < time.Millisecond {
		retry = time.Millisecond
	}
	return Result{RetryAfter: retry}, nil
}

func (l *PG) Add(ctx context.Context, key string, p Policy) error {
	if off, err := p.check(); err != nil || off {
		return err
	}
	if err := l.q.Add(ctx, ratelimitdb.AddParams{Key: key, Now: l.clock.Now(), IntervalS: p.interval().Seconds()}); err != nil {
		return fmt.Errorf("ratelimit: %w", err)
	}
	return nil
}

func (l *PG) Over(ctx context.Context, key string, p Policy) (bool, error) {
	if off, err := p.check(); err != nil || off {
		return false, err
	}
	tat, err := l.q.GetTAT(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("ratelimit: %w", err)
	}
	return tat.Sub(l.clock.Now()) > p.window()-p.interval(), nil
}

type unlimited struct{}

func (unlimited) Allow(context.Context, string, Policy) (Result, error) {
	return Result{Allowed: true}, nil
}
func (unlimited) Add(context.Context, string, Policy) error          { return nil }
func (unlimited) Over(context.Context, string, Policy) (bool, error) { return false, nil }

// Unlimited — всё пропускает: тесты хендлеров без базы.
var Unlimited Limiter = unlimited{}
