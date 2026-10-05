package geo_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/geo"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/testkit/dbtest"
)

var (
	eventCity   = uuid.MustParse("01920000-0000-7000-8000-000000000001")
	eventImport = uuid.MustParse("01920000-0000-7000-8000-000000000002")
)

// Конверт и payload — контракт для подписчиков (спека §6): имена полей JSON не меняются.
func TestEventShapes(t *testing.T) {
	cases := []struct {
		e                     events.Event
		typ, aggType, payload string
		aggID                 uuid.UUID
		aggVersion            int64
	}{
		{geo.CityLinkedEvent(eventCity, 3, 487846), "geo.city_linked", "city",
			`{"city_id":"01920000-0000-7000-8000-000000000001","geoname_id":487846}`, eventCity, 3},
		{geo.ImportFinishedEvent(eventImport, "RU", "succeeded"), "geo.import_finished", "geonames_import",
			`{"import_id":"01920000-0000-7000-8000-000000000002","country_code":"RU","status":"succeeded"}`, eventImport, 1},
	}
	for _, c := range cases {
		e := c.e
		if e.Type != c.typ || e.SchemaVersion != 1 || e.AggregateType != c.aggType ||
			e.AggregateID != c.aggID || e.AggregateVersion != c.aggVersion {
			t.Errorf("%s: конверт %+v", c.typ, e)
		}
		b, err := json.Marshal(e.Payload)
		if err != nil || string(b) != c.payload {
			t.Errorf("%s: payload %s (%v), ждали %s", c.typ, b, err, c.payload)
		}
	}
}

// События проходят проверку конверта платформы и пишутся в outbox.
func TestEventsPublish(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	err := db.InTx(ctx, pool, func(ctx context.Context, _ pgx.Tx) error {
		for _, e := range []events.Event{
			geo.CityLinkedEvent(eventCity, 2, 493702),
			geo.ImportFinishedEvent(eventImport, "RU", "failed"),
		} {
			if _, err := events.Publish(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type LIKE 'geo.%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("в outbox %d событий geo, ждали 2", n)
	}
}
