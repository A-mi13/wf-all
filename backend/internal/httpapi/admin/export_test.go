package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// ApproveStaffCodes — коды staff.*, которыми отвечает approve (страж сверки с x-error-codes).
var ApproveStaffCodes = approveStaffCodes

// NewHandlerWithSpec — конвейер на подменённом контракте: отказ старта на испорченном бандле
// (только тесты).
var NewHandlerWithSpec = newHandler

// Permissions — слой прав x-permission на правилах из spec (только тесты): {cityId} в пути на
// контракте плана A не встречается и проверяется на подменённом.
func Permissions(spec *openapi3.T, log *slog.Logger) (func(http.Handler) http.Handler, error) {
	rules, err := buildPermissions(spec)
	if err != nil {
		return nil, err
	}
	return permissions(rules, log), nil
}

// Wake — звено побудки api (только тесты).
var Wake = wake

// SetWakeGap — минимальный промежуток между побудками (тесты: 0 — без троттлинга по времени; «один в полёте» остаётся).
func SetWakeGap(w *Waker, d time.Duration) { w.gap = d }

// WakePokes — синхронный счётчик вызовов Poke: растёт в самом вызове, до запуска запроса.
func WakePokes(w *Waker) int64 { return w.pokes.Load() }

// WakeIdle — нет запроса побудки в полёте.
func WakeIdle(w *Waker) bool { return !w.busy.Load() }
