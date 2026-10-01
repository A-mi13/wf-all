// Package events — события модулей: outbox → relay → River → inbox (спека бэкенда §6.5).
// Модуль публикует событие в своей бизнес-транзакции (Publish), relay воркера раскладывает его
// подписчикам; эффект обработки — ровно один раз (обработчик может выполниться повторно,
// отметка event_inbox и изменения коммитятся вместе), а подписчик, следящий за состоянием
// агрегата, отбрасывает устаревшие версии (event_cursors).
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
	"wf/backend/internal/platform/id"
)

// ErrInvalidEvent — событие не прошло проверку конверта.
var ErrInvalidEvent = errors.New("events: невалидное событие")

// <модуль>.<имя> — у событий (факт в прошедшем времени) и у подписчиков
var dotted = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Event — то, что публикует модуль. Payload — только идентификаторы и непрофильные значения,
// без персональных данных (§4.6); сериализуется в JSON-объект.
type Event struct {
	Type             string
	SchemaVersion    int
	AggregateType    string
	AggregateID      uuid.UUID
	AggregateVersion int64
	Payload          any
}

// Envelope — событие, как его получает подписчик.
type Envelope struct {
	ID               uuid.UUID
	Type             string
	SchemaVersion    int
	AggregateType    string
	AggregateID      uuid.UUID
	AggregateVersion int64
	OccurredAt       time.Time
	Payload          json.RawMessage
}

// Publish пишет событие в outbox транзакцией из ctx (db.InTx): событие уходит, только если
// закоммитились данные. occurred_at — время транзакции из базы.
func Publish(ctx context.Context, e Event) (uuid.UUID, error) {
	tx, ok := db.TxFrom(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoTx
	}
	payload, err := validate(e)
	if err != nil {
		return uuid.Nil, err
	}
	eventID := id.New()
	err = eventsdb.New(tx).InsertEvent(ctx, eventsdb.InsertEventParams{
		ID:               eventID,
		EventType:        e.Type,
		SchemaVersion:    int32(e.SchemaVersion), //nolint:gosec // проверено validate: 1..MaxInt32
		AggregateType:    e.AggregateType,
		AggregateID:      e.AggregateID,
		AggregateVersion: e.AggregateVersion,
		Payload:          payload,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("events: outbox: %w", err)
	}
	return eventID, nil
}

func validate(e Event) ([]byte, error) {
	switch {
	case !dotted.MatchString(e.Type):
		return nil, fmt.Errorf("%w: тип %q — нужен <модуль>.<факт>", ErrInvalidEvent, e.Type)
	case e.SchemaVersion < 1 || e.SchemaVersion > 1<<31-1:
		return nil, fmt.Errorf("%w: версия схемы %d", ErrInvalidEvent, e.SchemaVersion)
	case e.AggregateType == "":
		return nil, fmt.Errorf("%w: пустой тип агрегата", ErrInvalidEvent)
	case e.AggregateID == uuid.Nil:
		return nil, fmt.Errorf("%w: нулевой id агрегата", ErrInvalidEvent)
	case e.AggregateVersion < 1:
		return nil, fmt.Errorf("%w: версия агрегата %d", ErrInvalidEvent, e.AggregateVersion)
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrInvalidEvent, err) //nolint:errorlint // причина — текстом, тип ошибки — ErrInvalidEvent
	}
	if len(payload) == 0 || payload[0] != '{' {
		return nil, fmt.Errorf("%w: payload — не JSON-объект", ErrInvalidEvent)
	}
	return payload, nil
}

func envelope(o eventsdb.Outbox) Envelope {
	return Envelope{
		ID: o.ID, Type: o.EventType, SchemaVersion: int(o.SchemaVersion),
		AggregateType: o.AggregateType, AggregateID: o.AggregateID, AggregateVersion: o.AggregateVersion,
		OccurredAt: o.OccurredAt, Payload: o.Payload,
	}
}
