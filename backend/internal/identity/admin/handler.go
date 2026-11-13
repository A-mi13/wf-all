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
	"net/http"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
)

// Handler — админские ручки identity. Нулевое значение — заглушка: каждая операция отвечает 501, пока этап спеки её
// не реализует (тогда метод получает зависимости и сценарий, а операция уходит из pendingOps).
type Handler struct{}

// errNotImplemented — ответ операции, которую этап спеки ещё не реализовал.
var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

// Заявки в капитаны, поиск игрока, сброс 2FA, статистика способов входа (план I-1, Task 4; спека identity §7.1) —
// реализует этап 9 спеки.

// ListCaptainApplications — GET /v1/identity/captain-applications: очередь заявок в капитаны.
func (Handler) ListCaptainApplications(context.Context, oapi.ListCaptainApplicationsRequestObject) (oapi.ListCaptainApplicationsResponseObject, error) {
	return nil, errNotImplemented
}

// GetCaptainApplication — GET /v1/identity/captain-applications/{applicationId}: карточка заявки.
func (Handler) GetCaptainApplication(context.Context, oapi.GetCaptainApplicationRequestObject) (oapi.GetCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// GetCaptainApplicationContact — GET /v1/identity/captain-applications/{applicationId}/contact: контакты заявителя.
func (Handler) GetCaptainApplicationContact(context.Context, oapi.GetCaptainApplicationContactRequestObject) (oapi.GetCaptainApplicationContactResponseObject, error) {
	return nil, errNotImplemented
}

// ApproveCaptainApplication — POST /v1/identity/captain-applications/{applicationId}/approve: одобрить заявку.
func (Handler) ApproveCaptainApplication(context.Context, oapi.ApproveCaptainApplicationRequestObject) (oapi.ApproveCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// RejectCaptainApplication — POST /v1/identity/captain-applications/{applicationId}/reject: отклонить заявку.
func (Handler) RejectCaptainApplication(context.Context, oapi.RejectCaptainApplicationRequestObject) (oapi.RejectCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// GetAuthMethodsUsage — GET /v1/identity/auth-methods/usage: сколько игроков входят каждым способом.
func (Handler) GetAuthMethodsUsage(context.Context, oapi.GetAuthMethodsUsageRequestObject) (oapi.GetAuthMethodsUsageResponseObject, error) {
	return nil, errNotImplemented
}

// FindPlayerByEmail — GET /v1/identity/users/lookup: найти игрока по почте.
func (Handler) FindPlayerByEmail(context.Context, oapi.FindPlayerByEmailRequestObject) (oapi.FindPlayerByEmailResponseObject, error) {
	return nil, errNotImplemented
}

// RequestMfaReset — POST /v1/identity/users/{userId}/mfa-reset: запросить сброс 2FA игрока.
func (Handler) RequestMfaReset(context.Context, oapi.RequestMfaResetRequestObject) (oapi.RequestMfaResetResponseObject, error) {
	return nil, errNotImplemented
}

// CancelMfaReset — POST /v1/identity/mfa-resets/{resetId}/cancel: отменить назначенный сброс 2FA.
func (Handler) CancelMfaReset(context.Context, oapi.CancelMfaResetRequestObject) (oapi.CancelMfaResetResponseObject, error) {
	return nil, errNotImplemented
}
