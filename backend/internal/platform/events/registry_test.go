package events_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/events"
)

func noop(context.Context, pgx.Tx, events.Envelope) error { return nil }

func sub(name, typ string, d events.Delivery) events.Subscription {
	return events.Subscription{Subscriber: name, EventType: typ, Delivery: d, Handle: noop}
}

func TestRegistryRoutesByType(t *testing.T) {
	r, err := events.NewRegistry(
		sub("stats.count_members", "teams.member_joined", events.EveryEvent),
		sub("notify.roster", "teams.member_joined", events.LatestState),
		sub("notify.roster", "teams.member_left", events.LatestState),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.For("teams.member_joined")); got != 2 {
		t.Fatalf("подписчиков member_joined = %d", got)
	}
	if got := len(r.For("teams.unknown")); got != 0 {
		t.Fatalf("подписчиков неизвестного типа = %d", got)
	}
	if s, ok := r.Get("notify.roster", "teams.member_left"); !ok || s.Delivery != events.LatestState {
		t.Fatalf("Get = %+v, %v", s, ok)
	}
	if _, ok := r.Get("notify.roster", "teams.captain_changed"); ok {
		t.Fatal("Get нашёл несуществующую подписку")
	}
}

// Подсадка ошибок регистрации: каждая ловится своим правилом.
func TestRegistryRejectsInvalid(t *testing.T) {
	cases := map[string]struct {
		subs []events.Subscription
		want string
	}{
		"режим доставки не задан":   {[]events.Subscription{sub("stats.x", "teams.member_joined", 0)}, "режим доставки"},
		"имя подписчика без модуля": {[]events.Subscription{sub("count", "teams.member_joined", events.EveryEvent)}, "подписчик"},
		"тип события без модуля":    {[]events.Subscription{sub("stats.x", "member_joined", events.EveryEvent)}, "тип события"},
		"нет обработчика":           {[]events.Subscription{{Subscriber: "stats.x", EventType: "teams.member_joined", Delivery: events.EveryEvent}}, "обработчик"},
		"дубль подписки": {[]events.Subscription{
			sub("stats.x", "teams.member_joined", events.EveryEvent),
			sub("stats.x", "teams.member_joined", events.LatestState),
		}, "дважды"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := events.NewRegistry(c.subs...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, ждали «%s»", err, c.want)
			}
		})
	}
}
