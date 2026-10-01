package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "WORKER_DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
}

// Пачка 0 зациклила бы Drain, нулевой опрос уронил бы NewTicker паникой: воркер отказывается
// стартовать до подключения к базе и называет ключ окружения.
func TestRunRejectsInvalidRelayConfig(t *testing.T) {
	// Адрес недостижим: если проверка не сработает, ошибка будет о подключении, не о ключе.
	base := "WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"
	for _, tc := range []struct{ kv, key string }{
		{"WORKER_RELAY_BATCH=0", "WORKER_RELAY_BATCH"},
		{"WORKER_RELAY_BATCH=-5", "WORKER_RELAY_BATCH"},
		{"WORKER_RELAY_POLL=0s", "WORKER_RELAY_POLL"},
		{"WORKER_RELAY_POLL=-1s", "WORKER_RELAY_POLL"},
	} {
		t.Run(tc.kv, func(t *testing.T) {
			err := run(context.Background(), []string{base, tc.kv}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("err = %v, ждали ошибку с %s", err, tc.key)
			}
		})
	}
}

// Воркер поднимает relay: событие, лежавшее в outbox, становится опубликованным; по отмене
// ctx воркер останавливается без ошибки.
func TestRunPublishesPendingEventsAndStops(t *testing.T) {
	url := dbtest.NewURL(t)
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		_, err := events.Publish(ctx, events.Event{
			Type: "teams.member_joined", SchemaVersion: 1, AggregateType: "team",
			AggregateID: id.New(), AggregateVersion: 1, Payload: map[string]string{},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"WORKER_DATABASE_URL=" + url}, io.Discard) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var left int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay воркера не опубликовал событие")
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка: %v", err)
	}
}
