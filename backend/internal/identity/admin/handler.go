// Package admin — ручки модуля identity в админке (тег identity контракта admin, спека identity §7): заявки в
// капитаны, поиск игрока по почте, сброс 2FA поддержкой, статистика способов входа. Handler встраивается в
// internal/httpapi/admin.Server: тег операции = модуль-реализатор (apitest.TagViolations).
//
// План I-1 (контракт до кода): нулевое значение Handler — каркас, каждая операция отвечает 501
// http.not_implemented, пока этап спеки её не реализует; такие операции — в pendingOps стража TestNoStubOperations
// (internal/httpapi/admin).
package admin

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
)

// Deps — зависимости админских сценариев identity у admin-api: пул роли admin, часы, клиент River только для вставки
// (письма identity — mail.SendArgs в очередь mail, исполняет воркер; R21), журнал.
type Deps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	River *river.Client[pgx.Tx]
	Log   *slog.Logger
}

// Handler — админские ручки identity. Нулевое значение — заглушка: каждая операция отвечает 501, пока этап спеки её
// не реализует. Собранный New — сброс 2FA поддержкой (Task 17 плана I-2); остальные операции — по-прежнему 501
// (pendingOps), их реализует этап 9 спеки.
type Handler struct {
	svc       identity.Service
	app       *app.App           // nil — заглушка
	approvals *approvals.Service // RegisterApprovals; nil — запрос сброса отвечает 501
}

// New — админские ручки identity на зависимостях admin-api (кольца identity у admin-api нет, N3).
func New(svc identity.Service, d Deps) (*Handler, error) {
	a, err := app.NewMaintenance(d.Pool, d.Clock, nil, d.River, d.Log)
	if err != nil {
		return nil, err
	}
	return &Handler{svc: svc, app: a}, nil
}

// errNotImplemented — ответ операции, которую этап спеки ещё не реализовал.
var (
	errNotImplemented  = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)
	errUnauthenticated = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)
	errNoBody          = httpx.NewFieldError("body", "required") // тело обязательно — валидатор отверг бы раньше
)

// Заявки в капитаны, поиск игрока, сброс 2FA, статистика способов входа (план I-1, Task 4; спека identity §7.1) —
// реализует этап 9 спеки.

// ListCaptainApplications — GET /v1/identity/captain-applications: очередь заявок в капитаны.
func (*Handler) ListCaptainApplications(context.Context, oapi.ListCaptainApplicationsRequestObject) (oapi.ListCaptainApplicationsResponseObject, error) {
	return nil, errNotImplemented
}

// GetCaptainApplication — GET /v1/identity/captain-applications/{applicationId}: карточка заявки.
func (*Handler) GetCaptainApplication(context.Context, oapi.GetCaptainApplicationRequestObject) (oapi.GetCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// GetCaptainApplicationContact — GET /v1/identity/captain-applications/{applicationId}/contact: контакты заявителя.
func (*Handler) GetCaptainApplicationContact(context.Context, oapi.GetCaptainApplicationContactRequestObject) (oapi.GetCaptainApplicationContactResponseObject, error) {
	return nil, errNotImplemented
}

// ApproveCaptainApplication — POST /v1/identity/captain-applications/{applicationId}/approve: одобрить заявку.
func (*Handler) ApproveCaptainApplication(context.Context, oapi.ApproveCaptainApplicationRequestObject) (oapi.ApproveCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// RejectCaptainApplication — POST /v1/identity/captain-applications/{applicationId}/reject: отклонить заявку.
func (*Handler) RejectCaptainApplication(context.Context, oapi.RejectCaptainApplicationRequestObject) (oapi.RejectCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// GetAuthMethodsUsage — GET /v1/identity/auth-methods/usage: сколько игроков входят каждым способом.
func (*Handler) GetAuthMethodsUsage(context.Context, oapi.GetAuthMethodsUsageRequestObject) (oapi.GetAuthMethodsUsageResponseObject, error) {
	return nil, errNotImplemented
}

// FindPlayerByEmail — GET /v1/identity/users/lookup: найти игрока по почте.
func (*Handler) FindPlayerByEmail(context.Context, oapi.FindPlayerByEmailRequestObject) (oapi.FindPlayerByEmailResponseObject, error) {
	return nil, errNotImplemented
}
