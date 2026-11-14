package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: капитанство — план I-4 (заглушки 501) (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// GetMyCaptaincy — GET /v1/me/captaincy: моё капитанство.
func (Handler) GetMyCaptaincy(context.Context, oapi.GetMyCaptaincyRequestObject) (oapi.GetMyCaptaincyResponseObject, error) {
	return nil, errNotImplemented
}

// ListMyCaptainApplications — GET /v1/me/captain-applications: мои заявки в капитаны.
func (Handler) ListMyCaptainApplications(context.Context, oapi.ListMyCaptainApplicationsRequestObject) (oapi.ListMyCaptainApplicationsResponseObject, error) {
	return nil, errNotImplemented
}

// SubmitCaptainApplication — POST /v1/captain-applications: подать заявку в капитаны.
func (Handler) SubmitCaptainApplication(context.Context, oapi.SubmitCaptainApplicationRequestObject) (oapi.SubmitCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}

// WithdrawCaptainApplication — POST /v1/captain-applications/{applicationId}/withdraw: отозвать заявку в капитаны.
func (Handler) WithdrawCaptainApplication(context.Context, oapi.WithdrawCaptainApplicationRequestObject) (oapi.WithdrawCaptainApplicationResponseObject, error) {
	return nil, errNotImplemented
}
