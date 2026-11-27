package media

import (
	"net/http"

	"wf/backend/internal/platform/httpx"
)

// Коды ошибок media (спека §7.2, R18): своих операций у media нет — коды отдают операции владельцев (начать и
// завершить загрузку, прямая загрузка). В x-error-codes операций и errors.media.* локалей фронтов они входят с первой
// операцией владельца (план economy); здесь — полнота списка (TestCodeConstantsListed).
const (
	CodeUnavailable       = "media.unavailable"        // 503: режим хранилища none (§6.4)
	CodeUnsupportedType   = "media.unsupported_type"   // 422: заявленный тип не из входов цели
	CodeTooLarge          = "media.too_large"          // 422: заявленный размер больше предела цели
	CodeTooManyPending    = "media.too_many_pending"   // 429: больше 5 незавершённых билетов (§3.11)
	CodeQuotaExceeded     = "media.quota_exceeded"     // 429: суточный объём игрока (§3.11)
	CodeUploadIncomplete  = "media.upload_incomplete"  // 409: «завершить» без файла во входящем
	CodeUploadExpired     = "media.upload_expired"     // 410: окно «завершить» прошло
	CodeObjectUnavailable = "media.object_unavailable" // 409: объект не годится для прикрепления (§2.3)
)

// OwnerCodes — коды media, которые отдают операции владельцев.
var OwnerCodes = []string{
	CodeUnavailable, CodeUnsupportedType, CodeTooLarge, CodeTooManyPending, CodeQuotaExceeded, CodeUploadIncomplete,
	CodeUploadExpired, CodeObjectUnavailable,
}

// Ошибки-значения для владельцев: ручка отдаёт их как есть (httpx.ProblemError).
var (
	ErrUnavailable       = httpx.NewError(http.StatusServiceUnavailable, CodeUnavailable)
	ErrUnsupportedType   = httpx.NewError(http.StatusUnprocessableEntity, CodeUnsupportedType)
	ErrTooLarge          = httpx.NewError(http.StatusUnprocessableEntity, CodeTooLarge)
	ErrTooManyPending    = httpx.NewError(http.StatusTooManyRequests, CodeTooManyPending)
	ErrQuotaExceeded     = httpx.NewError(http.StatusTooManyRequests, CodeQuotaExceeded)
	ErrUploadIncomplete  = httpx.NewError(http.StatusConflict, CodeUploadIncomplete)
	ErrUploadExpired     = httpx.NewError(http.StatusGone, CodeUploadExpired)
	ErrObjectUnavailable = httpx.NewError(http.StatusConflict, CodeObjectUnavailable)
)
