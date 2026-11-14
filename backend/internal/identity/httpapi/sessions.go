package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: «Где вы вошли» и способы входа — владелец Task 12 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// GetAuthMethods — GET /v1/auth/methods: способы входа.
func (Handler) GetAuthMethods(context.Context, oapi.GetAuthMethodsRequestObject) (oapi.GetAuthMethodsResponseObject, error) {
	return nil, errNotImplemented
}

// ListMySessions — GET /v1/me/sessions: «Где вы вошли».
func (Handler) ListMySessions(context.Context, oapi.ListMySessionsRequestObject) (oapi.ListMySessionsResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeMySession — DELETE /v1/me/sessions/{sessionId}: выйти на устройстве.
func (Handler) RevokeMySession(context.Context, oapi.RevokeMySessionRequestObject) (oapi.RevokeMySessionResponseObject, error) {
	return nil, errNotImplemented
}

// RevokeOtherSessions — POST /v1/me/sessions/revoke-others: выйти везде, кроме этого устройства.
func (Handler) RevokeOtherSessions(context.Context, oapi.RevokeOtherSessionsRequestObject) (oapi.RevokeOtherSessionsResponseObject, error) {
	return nil, errNotImplemented
}
