package geo

import (
	"github.com/google/uuid"

	"wf/backend/internal/platform/events"
)

// События модуля geo (спека geo §6) — контракт для подписчиков. Payload — только
// идентификаторы и непрофильные значения, схема v1. Подписчиков пока нет.
const (
	EventCityActivated  = "geo.city_activated"
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

// CityActivated — payload geo.city_activated: город заведён из места GeoNames (активация, Task 10).
// RegionID nil — регион не задан (null).
type CityActivated struct {
	CityID      uuid.UUID  `json:"city_id"`
	CountryCode string     `json:"country_code"`
	RegionID    *uuid.UUID `json:"region_id"`
	Status      Status     `json:"status"`
	GeonameID   int64      `json:"geoname_id"`
}

// CityActivatedEvent — событие активации; версия агрегата — 1 (новый город, cities.version = 1).
func CityActivatedEvent(cityID uuid.UUID, countryCode string, regionID *uuid.UUID, status Status, geonameID int64) events.Event {
	return events.Event{
		Type: EventCityActivated, SchemaVersion: 1,
		AggregateType: AggregateCity, AggregateID: cityID, AggregateVersion: 1,
		Payload: CityActivated{CityID: cityID, CountryCode: countryCode, RegionID: regionID, Status: status, GeonameID: geonameID},
	}
}

// Правка города админкой (Task 11; спека geo §6): событие на каждый вид изменения, версии агрегата —
// последовательные в транзакции (R11).
const (
	EventCityStatusChanged = "geo.city_status_changed"
	EventCityRenamed       = "geo.city_renamed"
	EventCitySlugChanged   = "geo.city_slug_changed"
)

// CityStatusChanged — payload geo.city_status_changed: статус города сменился с From на To.
type CityStatusChanged struct {
	CityID uuid.UUID `json:"city_id"`
	From   Status    `json:"from"`
	To     Status    `json:"to"`
}

// CityRenamed — payload geo.city_renamed: изменились (добавлены, переписаны или удалены) названия на языках
// Locales (по алфавиту).
type CityRenamed struct {
	CityID  uuid.UUID `json:"city_id"`
	Locales []string  `json:"locales"`
}

// CitySlugChanged — payload geo.city_slug_changed: адрес города сменился, старый остался в истории.
type CitySlugChanged struct {
	CityID  uuid.UUID `json:"city_id"`
	OldSlug string    `json:"old_slug"`
	NewSlug string    `json:"new_slug"`
}

// CityStatusChangedEvent — событие смены статуса; cityVersion — версия города после этого изменения.
func CityStatusChangedEvent(cityID uuid.UUID, cityVersion int64, from, to Status) events.Event {
	return events.Event{
		Type: EventCityStatusChanged, SchemaVersion: 1,
		AggregateType: AggregateCity, AggregateID: cityID, AggregateVersion: cityVersion,
		Payload: CityStatusChanged{CityID: cityID, From: from, To: to},
	}
}

// CityRenamedEvent — событие правки названий; locales — изменённые языки по алфавиту.
func CityRenamedEvent(cityID uuid.UUID, cityVersion int64, locales []string) events.Event {
	return events.Event{
		Type: EventCityRenamed, SchemaVersion: 1,
		AggregateType: AggregateCity, AggregateID: cityID, AggregateVersion: cityVersion,
		Payload: CityRenamed{CityID: cityID, Locales: locales},
	}
}

// CitySlugChangedEvent — событие смены адреса.
func CitySlugChangedEvent(cityID uuid.UUID, cityVersion int64, oldSlug, newSlug string) events.Event {
	return events.Event{
		Type: EventCitySlugChanged, SchemaVersion: 1,
		AggregateType: AggregateCity, AggregateID: cityID, AggregateVersion: cityVersion,
		Payload: CitySlugChanged{CityID: cityID, OldSlug: oldSlug, NewSlug: newSlug},
	}
}

// Районы (Task 12; спека geo §6): агрегат district, версия — districts.version.
const (
	EventDistrictCreated  = "geo.district_created"
	EventDistrictUpdated  = "geo.district_updated"
	EventDistrictArchived = "geo.district_archived"

	AggregateDistrict = "district"
)

// DistrictChanged — payload событий района: какой район какого города.
type DistrictChanged struct {
	DistrictID uuid.UUID `json:"district_id"`
	CityID     uuid.UUID `json:"city_id"`
}

// DistrictEvent — событие района типа typ (EventDistrict…); version — версия района после изменения.
func DistrictEvent(typ string, districtID, cityID uuid.UUID, version int64) events.Event {
	return events.Event{
		Type: typ, SchemaVersion: 1,
		AggregateType: AggregateDistrict, AggregateID: districtID, AggregateVersion: version,
		Payload: DistrictChanged{DistrictID: districtID, CityID: cityID},
	}
}
