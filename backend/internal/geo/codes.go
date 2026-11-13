package geo

// Коды ошибок модуля для клиентов: x-error-codes операций geo в контрактах, тексты — errors.<код>
// в локалях фронтов (публичные — apps/web, админские — apps/admin). Сверку держат
// TestGeoCodesDocumented (internal/httpapi/public, PublicCodes) и TestGeoAdminCodesDocumented
// (internal/httpapi/admin, AdminCodes) — в обе стороны.
const (
	CodeCityNotFound = "geo.city_not_found" // 404: города нет (в публичном API — или его страна выключена)

	// админка (план B, Приложение 3; спека geo §8, решения R4, R22)
	CodeDistrictNotFound        = "geo.district_not_found"         // 404: района нет или он не из города пути
	CodePlaceNotFound           = "geo.place_not_found"            // 404: места GeoNames нет или оно пропало из источника
	CodeCountryNotFound         = "geo.country_not_found"          // 404: страны нет в справочнике
	CodeImportNotFound          = "geo.import_not_found"           // 404: строки журнала импорта нет
	CodeImportJobNotFound       = "geo.import_job_not_found"       // 404: задачи импорта нет или это не задача импорта
	CodeCityAlreadyActive       = "geo.city_already_active"        // 409: на месте GeoNames уже есть город
	CodeCityAlreadyLinked       = "geo.city_already_linked"        // 409: город уже привязан к месту GeoNames
	CodeSlugTaken               = "geo.slug_taken"                 // 409: адрес занят другим городом или его историей
	CodeCountryExists           = "geo.country_exists"             // 409: страна с этим кодом уже есть
	CodeImportInProgress        = "geo.import_in_progress"         // 409: импорт страны уже в очереди или идёт
	CodeImportJobNotCancellable = "geo.import_job_not_cancellable" // 409: задача импорта выполняется, выполнена или выброшена (уже отменённая — 200)
	CodeDistrictNameTaken       = "geo.district_name_taken"        // 409: название занято действующим районом города
	CodeCountryNotEnabled       = "geo.country_not_enabled"        // 422: страна активации выключена или её нет
	CodeRegionNotFound          = "geo.region_not_found"           // 422: region_id не из страны города
)

// PublicCodes — коды операций geo публичного контракта.
var PublicCodes = []string{CodeCityNotFound}

// AdminCodes — коды операций geo контракта admin.
var AdminCodes = []string{
	CodeCityNotFound, CodeDistrictNotFound, CodePlaceNotFound, CodeCountryNotFound,
	CodeImportNotFound, CodeImportJobNotFound,
	CodeCityAlreadyActive, CodeCityAlreadyLinked, CodeSlugTaken, CodeCountryExists,
	CodeImportInProgress, CodeImportJobNotCancellable, CodeDistrictNameTaken,
	CodeCountryNotEnabled, CodeRegionNotFound,
}
