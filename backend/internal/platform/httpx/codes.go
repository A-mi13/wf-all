package httpx

// Коды ошибок платформы: их может вернуть любая операция. Каждый код — константа здесь,
// запись в PlatformCodes, строка x-error-codes-common обоих контрактов и тексты errors.<код>
// в локалях веба и админки. Сверку с контрактом держит TestPlatformCodesDocumented
// в internal/httpapi/{public,admin}.
const (
	CodeInternal         = "internal"                // сбой сервера; детали — только в лог
	CodeRequestInvalid   = "request.invalid"         // запрос не разбирается
	CodeRequestTooLarge  = "request.too_large"       // тело больше лимита
	CodeValidationFailed = "validation.failed"       // нарушение схемы контракта, поля — в errors
	CodeNotFound         = "http.not_found"          // маршрута нет
	CodeMethodNotAllowed = "http.method_not_allowed" // маршрут есть, метода нет
)

// PlatformCodes — все коды платформы.
var PlatformCodes = []string{
	CodeInternal, CodeRequestInvalid, CodeRequestTooLarge,
	CodeValidationFailed, CodeNotFound, CodeMethodNotAllowed,
}
