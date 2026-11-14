package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Операции области: вход, обновление токенов, выход — владелец Task 11 плана I-2 (раскладка заглушек — Task 8, R5,
// Приложение 1). Пока сценарий не реализован, операция отвечает 501 и стоит в pendingOps.

// Login — POST /v1/auth/login: вход по почте и паролю.
func (Handler) Login(context.Context, oapi.LoginRequestObject) (oapi.LoginResponseObject, error) {
	return nil, errNotImplemented
}

// Logout — POST /v1/auth/logout: выйти.
func (Handler) Logout(context.Context, oapi.LogoutRequestObject) (oapi.LogoutResponseObject, error) {
	return nil, errNotImplemented
}

// RefreshSession — POST /v1/auth/refresh: обновить токены.
func (Handler) RefreshSession(context.Context, oapi.RefreshSessionRequestObject) (oapi.RefreshSessionResponseObject, error) {
	return nil, errNotImplemented
}
