package admin

import (
	"log/slog"
	"net/http"

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
