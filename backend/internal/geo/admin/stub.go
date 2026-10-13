// Package admin — операции модуля geo в админке (тег geo контракта admin). План A — список городов
// для выбора городов модератора (D24, P28, M10); план B расширяет.
package admin

import (
	"context"
	"net/http"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
)

// Handler — операции тега geo; встраивается в internal/httpapi/admin.Server.
//
// Заглушка Task 9a: контракт admin заморожен раньше сценариев (план A, P16) — операция отвечает
// 501 http.not_implemented, пока Task 10b не заменит её хендлером поверх чтения geo.Service.
type Handler struct{}

var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

// ListGeoCities — GET /v1/geo/cities.
func (Handler) ListGeoCities(context.Context, oapi.ListGeoCitiesRequestObject) (oapi.ListGeoCitiesResponseObject, error) {
	return nil, errNotImplemented
}
