package geo

import (
	"github.com/google/uuid"

	"wf/backend/internal/platform/events"
)

// События модуля geo (спека geo §6) — контракт для подписчиков. Payload — только
// идентификаторы и непрофильные значения, схема v1. Подписчиков пока нет.
const (
	EventCityLinked     = "geo.city_linked"
	EventImportFinished = "geo.import_finished"

	AggregateCity           = "city"
	AggregateGeonamesImport = "geonames_import"
)

// CityLinked — payload geo.city_linked: сверка импорта связала заведённый город с местом GeoNames.
type CityLinked struct {
	CityID    uuid.UUID `json:"city_id"`
	GeonameID int64     `json:"geoname_id"`
}

// ImportFinished — payload geo.import_finished: итог импорта (status — succeeded | failed).
type ImportFinished struct {
	ImportID    uuid.UUID `json:"import_id"`
	CountryCode string    `json:"country_code"`
	Status      string    `json:"status"`
}

// CityLinkedEvent — событие для events.Publish; cityVersion — cities.version после изменения.
func CityLinkedEvent(cityID uuid.UUID, cityVersion, geonameID int64) events.Event {
	return events.Event{
		Type: EventCityLinked, SchemaVersion: 1,
		AggregateType: AggregateCity, AggregateID: cityID, AggregateVersion: cityVersion,
		Payload: CityLinked{CityID: cityID, GeonameID: geonameID},
	}
}

// ImportFinishedEvent — событие итога импорта; версия агрегата всегда 1 (строка журнала
// завершается один раз, спека §6).
func ImportFinishedEvent(importID uuid.UUID, countryCode, status string) events.Event {
	return events.Event{
		Type: EventImportFinished, SchemaVersion: 1,
		AggregateType: AggregateGeonamesImport, AggregateID: importID, AggregateVersion: 1,
		Payload: ImportFinished{ImportID: importID, CountryCode: countryCode, Status: status},
	}
}
