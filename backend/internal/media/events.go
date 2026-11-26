package media

import (
	"github.com/google/uuid"

	"wf/backend/internal/platform/events"
)

// События media (спека §9): агрегат media_object, версия — media_objects.version после изменения; подписчики —
// владельцы в режиме LatestState. Без адресов, ключей хранилища и «кто загрузил» (TestEventPayloadsNoPII).
const (
	EventObjectReady     = "media.object_ready"
	EventObjectRejected  = "media.object_rejected"
	EventObjectTakenDown = "media.object_taken_down"
	AggregateObject      = "media_object"
	eventSchemaVersion   = 1
)

// EventTypes — все события модуля (сверка каталога в тестах).
func EventTypes() []string {
	return []string{EventObjectReady, EventObjectRejected, EventObjectTakenDown}
}

// ObjectRef — агрегат media_object: id и media_objects.version после изменения в той же tx.
type ObjectRef struct {
	ID      uuid.UUID
	Version int64
}

// ObjectPayload — media.object_ready и media.object_taken_down.
type ObjectPayload struct {
	ObjectID  uuid.UUID `json:"object_id"`
	Purpose   string    `json:"purpose"`
	OwnerType string    `json:"owner_type"`
	OwnerID   uuid.UUID `json:"owner_id"`
}

// ObjectRejectedPayload — media.object_rejected: плюс код причины.
type ObjectRejectedPayload struct {
	ObjectID   uuid.UUID  `json:"object_id"`
	Purpose    string     `json:"purpose"`
	OwnerType  string     `json:"owner_type"`
	OwnerID    uuid.UUID  `json:"owner_id"`
	RejectCode RejectCode `json:"reject_code"`
}

func objectEvent(typ string, o ObjectRef, payload any) events.Event {
	return events.Event{Type: typ, SchemaVersion: eventSchemaVersion, AggregateType: AggregateObject,
		AggregateID: o.ID, AggregateVersion: o.Version, Payload: payload}
}

// ObjectReady — объект обработан и опубликован (processing → ready).
func ObjectReady(o ObjectRef, purpose string, owner OwnerRef) events.Event {
	return objectEvent(EventObjectReady, o,
		ObjectPayload{ObjectID: o.ID, Purpose: purpose, OwnerType: owner.Type, OwnerID: owner.ID})
}

// ObjectRejected — объект отклонён воркером с причиной.
func ObjectRejected(o ObjectRef, purpose string, owner OwnerRef, code RejectCode) events.Event {
	return objectEvent(EventObjectRejected, o, ObjectRejectedPayload{ObjectID: o.ID, Purpose: purpose,
		OwnerType: owner.Type, OwnerID: owner.ID, RejectCode: code})
}

// ObjectTakenDown — объект снят (TakeDownInTx).
func ObjectTakenDown(o ObjectRef, purpose string, owner OwnerRef) events.Event {
	return objectEvent(EventObjectTakenDown, o,
		ObjectPayload{ObjectID: o.ID, Purpose: purpose, OwnerType: owner.Type, OwnerID: owner.ID})
}
