package events

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Delivery — как подписчик относится к порядку событий. Порядок доставки между задачами не
// гарантирован даже внутри агрегата (§6.5), поэтому режим выбирается явно.
type Delivery int

const (
	_ Delivery = iota
	// EveryEvent — каждое событие применяется (счётчики, начисления): старое не отбрасывается.
	EveryEvent
	// LatestState — подписчик синхронизирует состояние агрегата: событие со строго меньшей
	// версией, чем уже применённая, отбрасывается (event_cursors). События одной версии
	// (одна транзакция агрегата) применяются все.
	LatestState
)

// Handler обрабатывает событие в транзакции вместе с отметкой event_inbox: ошибка откатывает
// и изменения подписчика, и отметку — задача River повторится. tx лежит и в ctx (db.TxFrom),
// поэтому обработчик может публиковать свои события.
type Handler func(ctx context.Context, tx pgx.Tx, e Envelope) error

// Subscription — подписчик (<модуль>.<имя>) на один тип события.
type Subscription struct {
	Subscriber string
	EventType  string
	Delivery   Delivery
	Handle     Handler
}

type subKey struct{ subscriber, eventType string }

// Registry — подписки воркера. Собирается один раз при старте; после — только читается.
type Registry struct {
	byType map[string][]Subscription
	byKey  map[subKey]Subscription
}

func NewRegistry(subs ...Subscription) (*Registry, error) {
	r := &Registry{byType: map[string][]Subscription{}, byKey: map[subKey]Subscription{}}
	var errs []error
	for _, s := range subs {
		k := subKey{s.Subscriber, s.EventType}
		switch {
		case !dotted.MatchString(s.Subscriber):
			errs = append(errs, fmt.Errorf("подписчик %q — нужен <модуль>.<имя>", s.Subscriber))
		case !dotted.MatchString(s.EventType):
			errs = append(errs, fmt.Errorf("%s: тип события %q — нужен <модуль>.<факт>", s.Subscriber, s.EventType))
		case s.Delivery != EveryEvent && s.Delivery != LatestState:
			errs = append(errs, fmt.Errorf("%s: не задан режим доставки (EveryEvent или LatestState)", s.Subscriber))
		case s.Handle == nil:
			errs = append(errs, fmt.Errorf("%s: нет обработчика", s.Subscriber))
		default:
			if _, dup := r.byKey[k]; dup {
				errs = append(errs, fmt.Errorf("%s подписан на %s дважды", s.Subscriber, s.EventType))
				continue
			}
			r.byKey[k] = s
			r.byType[s.EventType] = append(r.byType[s.EventType], s)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("events: реестр: %w", err)
	}
	return r, nil
}

// For — подписчики типа события.
func (r *Registry) For(eventType string) []Subscription { return r.byType[eventType] }

// Get — подписка подписчика на тип события.
func (r *Registry) Get(subscriber, eventType string) (Subscription, bool) {
	s, ok := r.byKey[subKey{subscriber, eventType}]
	return s, ok
}
