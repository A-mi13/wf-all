package media_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/events"
)

var (
	objectID = uuid.MustParse("01930000-0000-7000-8000-0000000000b1")
	ownerID  = uuid.MustParse("01930000-0000-7000-8000-0000000000b2")
	object   = media.ObjectRef{ID: objectID, Version: 4}
	owner    = media.OwnerRef{Type: "pitch_photo", ID: ownerID}
)

// allEvents — по конструктору на событие каталога §9.
func allEvents() map[string]events.Event {
	return map[string]events.Event{
		media.EventObjectReady:     media.ObjectReady(object, "pitch_photo", owner),
		media.EventObjectRejected:  media.ObjectRejected(object, "pitch_photo", owner, media.RejectCorrupt),
		media.EventObjectTakenDown: media.ObjectTakenDown(object, "pitch_photo", owner),
	}
}

// §9: без адресов, ключей и «кто загрузил» (и прочих ПД); payload — структура.
func TestEventPayloadsNoPII(t *testing.T) {
	t.Parallel()
	banned := []string{"uploaded_by", "email", "url", "key", "user", "staff", "ip"}
	for name, e := range allEvents() {
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for k := range m {
			for _, b := range banned {
				if k == b || strings.HasPrefix(k, b+"_") || strings.HasSuffix(k, "_"+b) || strings.Contains(k, "_"+b+"_") {
					t.Errorf("%s: ключ %q — ПД, адрес или ключ хранилища в payload", name, k)
				}
			}
		}
		if reflect.TypeOf(e.Payload).Kind() != reflect.Struct {
			t.Errorf("%s: payload — не структура", name)
		}
	}
}

// Конверт и payload по §9: агрегат media_object, версия — media_objects.version, схема v1; ключи payload — ровно
// object_id, purpose, owner_type, owner_id (+ reject_code у отклонения).
func TestEventPayloadShape(t *testing.T) {
	t.Parallel()
	base := `"object_id":"` + objectID.String() + `","purpose":"pitch_photo","owner_type":"pitch_photo","owner_id":"` +
		ownerID.String() + `"`
	want := map[string]string{
		media.EventObjectReady:     "{" + base + "}",
		media.EventObjectRejected:  "{" + base + `,"reject_code":"corrupt"}`,
		media.EventObjectTakenDown: "{" + base + "}",
	}
	for name, e := range allEvents() {
		if e.Type != name || media.AggregateObject != "media_object" || e.AggregateType != media.AggregateObject ||
			e.AggregateID != objectID || e.AggregateVersion != 4 || e.SchemaVersion != 1 {
			t.Errorf("%s: конверт %+v", name, e)
		}
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != want[name] {
			t.Errorf("%s: payload %s, ждали %s", name, raw, want[name])
		}
	}
}

// EventTypes — ровно события каталога §9 с префиксом media.
func TestEventTypes(t *testing.T) {
	t.Parallel()
	got := media.EventTypes()
	slices.Sort(got)
	want := []string{"media.object_ready", "media.object_rejected", "media.object_taken_down"}
	if !slices.Equal(got, want) {
		t.Fatalf("EventTypes = %v", got)
	}
	for _, typ := range got {
		if _, ok := allEvents()[typ]; !ok {
			t.Errorf("%s: нет конструктора в тесте", typ)
		}
	}
}
