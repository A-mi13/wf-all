package admin

import (
	"context"

	"wf/backend/internal/httpapi/admin/oapi"
)

// Заявки в капитаны (спека identity §3.12 п. 6, §7.1; план I-4): очередь, карточка, контакты заявителя, одобрение и
// отказ. Пока сценарий не реализован (план I-4, Task 4) — 501 http.not_implemented (pendingCaptains стража
// TestNoStubOperations).

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
