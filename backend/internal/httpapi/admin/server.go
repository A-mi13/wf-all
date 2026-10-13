package admin

import (
	"context"
	"net/http"

	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	staffadmin "wf/backend/internal/staff/admin"
)

// Server собирает API админки: встраивает хендлеры модулей (internal/<модуль>/admin),
// операции платформы (тег platform) реализует сам. Компилятор ловит и нереализованную
// операцию, и реализованную двумя модулями (неоднозначный метод) — спека §8.2.
// Хендлеры модулей встроены указателями: модуль строит *Handler конструктором, методы — с
// указателем-получателем (собирает NewHandler из Options.Staff и Options.Geo).
type Server struct {
	*staffHandler // тег staff: вход, сессии, «Мой доступ», роли и доступ сотрудников
	*geoHandler   // тег geo: города для выбора городов модератора
}

// Псевдонимы — имена встроенных полей: хендлеры модулей называются одинаково (Handler), а имя
// встроенного поля — имя типа. Модуль-реализатора apitest.Owners берёт из пакета типа.
type (
	staffHandler = staffadmin.Handler
	geoHandler   = geoadmin.Handler
)

var _ oapi.StrictServerInterface = Server{}

// Операции платформы (журнал действий, подтверждения вдвоём) — заглушки Task 9a: контракт admin
// заморожен раньше сценариев (план A, P16), операции отвечают 501 http.not_implemented, пока
// Task 10b не заменит их.
var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

// ApproveApproval — POST /v1/approvals/{approvalId}/approve.
func (Server) ApproveApproval(context.Context, oapi.ApproveApprovalRequestObject) (oapi.ApproveApprovalResponseObject, error) {
	return nil, errNotImplemented
}

// CancelApproval — POST /v1/approvals/{approvalId}/cancel.
func (Server) CancelApproval(context.Context, oapi.CancelApprovalRequestObject) (oapi.CancelApprovalResponseObject, error) {
	return nil, errNotImplemented
}

// ListApprovals — GET /v1/approvals.
func (Server) ListApprovals(context.Context, oapi.ListApprovalsRequestObject) (oapi.ListApprovalsResponseObject, error) {
	return nil, errNotImplemented
}

// ListAudit — GET /v1/audit.
func (Server) ListAudit(context.Context, oapi.ListAuditRequestObject) (oapi.ListAuditResponseObject, error) {
	return nil, errNotImplemented
}

// RejectApproval — POST /v1/approvals/{approvalId}/reject.
func (Server) RejectApproval(context.Context, oapi.RejectApprovalRequestObject) (oapi.RejectApprovalResponseObject, error) {
	return nil, errNotImplemented
}
