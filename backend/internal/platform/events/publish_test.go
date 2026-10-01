package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func joined(agg uuid.UUID, version int64) events.Event {
	return events.Event{
		Type: "teams.member_joined", SchemaVersion: 1,
		AggregateType: "team", AggregateID: agg, AggregateVersion: version,
		Payload: map[string]any{"team_id": agg.String(), "user_id": id.New().String()},
	}
}

func outboxCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPublishWritesEnvelopeInTransaction(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	agg := id.New()
	var eventID uuid.UUID
	err := db.InTx(ctx, pool, func(ctx context.Context, _ pgx.Tx) error {
		var err error
		eventID, err = events.Publish(ctx, joined(agg, 3))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if eventID.Version() != 7 {
		t.Fatalf("id события не UUIDv7: %s", eventID)
	}
	var (
		typ, aggType string
		aggID        uuid.UUID
		schema       int
		version      int64
		payload      []byte
		occurredSet  bool
		published    *string
	)
	err = pool.QueryRow(ctx, `SELECT event_type, aggregate_type, aggregate_id, schema_version,
		aggregate_version, payload, occurred_at IS NOT NULL, published_at::text FROM outbox WHERE id = $1`, eventID).
		Scan(&typ, &aggType, &aggID, &schema, &version, &payload, &occurredSet, &published)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "teams.member_joined" || aggType != "team" || aggID != agg || schema != 1 || version != 3 ||
		!occurredSet || published != nil {
		t.Fatalf("строка outbox: %s %s %s %d %d %v %v", typ, aggType, aggID, schema, version, occurredSet, published)
	}
	var p map[string]string
	if err := json.Unmarshal(payload, &p); err != nil || p["team_id"] != agg.String() {
		t.Fatalf("payload = %s (%v)", payload, err)
	}
}

func TestPublishOutsideTransactionFails(t *testing.T) {
	_, err := events.Publish(context.Background(), joined(id.New(), 1))
	if !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("err = %v, ждали db.ErrNoTx", err)
	}
}

// Review Focus 4: откат бизнес-транзакции уносит и событие.
func TestPublishRolledBackWithTransaction(t *testing.T) {
	pool := dbtest.NewPool(t)
	_ = db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		if _, err := events.Publish(ctx, joined(id.New(), 1)); err != nil {
			t.Fatal(err)
		}
		return errors.New("бизнес-ошибка")
	})
	if n := outboxCount(t, pool); n != 0 {
		t.Fatalf("после отката в outbox %d строк", n)
	}
}

func TestPublishRejectsInvalidEvent(t *testing.T) {
	pool := dbtest.NewPool(t)
	bad := map[string]func(e *events.Event){
		"тип без модуля":           func(e *events.Event) { e.Type = "member_joined" },
		"нулевая версия схемы":     func(e *events.Event) { e.SchemaVersion = 0 },
		"нулевая версия агрегата":  func(e *events.Event) { e.AggregateVersion = 0 },
		"пустой тип агрегата":      func(e *events.Event) { e.AggregateType = "" },
		"нулевой id агрегата":      func(e *events.Event) { e.AggregateID = uuid.Nil },
		"payload не объект":        func(e *events.Event) { e.Payload = []int{1, 2} },
		"payload не сериализуется": func(e *events.Event) { e.Payload = map[string]any{"f": func() {}} },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			e := joined(id.New(), 1)
			mutate(&e)
			err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
				_, err := events.Publish(ctx, e)
				return err
			})
			if !errors.Is(err, events.ErrInvalidEvent) {
				t.Fatalf("err = %v, ждали ErrInvalidEvent", err)
			}
		})
	}
}
