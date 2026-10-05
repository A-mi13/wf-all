package geo

// Коды ошибок модуля для клиентов: x-error-codes операций geo в контрактах, тексты — errors.<код>
// в локалях фронтов. Сверку с публичным контрактом держит TestGeoCodesDocumented
// (internal/httpapi/public); админские коды этапа 2 добавят сверку с admin.
const CodeCityNotFound = "geo.city_not_found" // 404: города нет или его страна выключена

// Codes — все коды модуля.
var Codes = []string{CodeCityNotFound}
