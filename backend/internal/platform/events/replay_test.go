package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestReplayEnqueuesForOneSubscriber(t *testing.T) {
	pool := dbtest.NewPool(t)
	for range 3 {
		publish(t, pool, joined(id.New(), 1))
	}
	publish(t, pool, left(id.New())) // другой тип — не переигрывается
	reg := twoSubscribers(t)
	ins := inserter(t, pool)
	since := time.Now().Add(-time.Hour)

	n, err := events.Replay(context.Background(), pool, ins, reg, "teams.member_joined", "notify.roster", since)
	if err != nil || n != 3 {
		t.Fatalf("Replay = %d, %v", n, err)
	}
	var forSub int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job
		WHERE kind = 'events.deliver' AND args->>'subscriber' = 'notify.roster'`).Scan(&forSub); err != nil {
		t.Fatal(err)
	}
	if forSub != 3 || deliveries(t, pool) != 3 {
		t.Fatalf("задач подписчику %d, всего %d — переигровка задела чужих", forSub, deliveries(t, pool))
	}
	// повтор не плодит дублей
	if n, err := events.Replay(context.Background(), pool, ins, reg, "teams.member_joined", "notify.roster", since); err != nil || n != 0 {
		t.Fatalf("повторный Replay = %d, %v", n, err)
	}
	// since в будущем — ничего
	if n, _ := events.Replay(context.Background(), pool, ins, reg, "teams.member_joined", "notify.roster", time.Now().Add(time.Hour)); n != 0 {
		t.Fatalf("Replay из будущего = %d", n)
	}
}

func TestReplayRejectsUnknownSubscription(t *testing.T) {
	pool := dbtest.NewPool(t)
	_, err := events.Replay(context.Background(), pool, inserter(t, pool), twoSubscribers(t),
		"teams.member_left", "notify.roster", time.Now())
	if !errors.Is(err, events.ErrUnknownSubscription) {
		t.Fatalf("err = %v", err)
	}
}
