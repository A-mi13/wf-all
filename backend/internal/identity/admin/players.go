package admin

import (
	"context"

	"wf/backend/internal/httpapi/admin/oapi"
)

// Поиск игрока по почте для поддержки и статистика способов входа (спека identity §7.1; план I-4). Пока сценарий не
// реализован (план I-4, Task 5) — 501 http.not_implemented (pendingPlayers стража TestNoStubOperations). Заголовки
// ПД — headers.go.

// FindPlayerByEmail — GET /v1/identity/users/lookup: найти игрока по почте.
func (*Handler) FindPlayerByEmail(context.Context, oapi.FindPlayerByEmailRequestObject) (oapi.FindPlayerByEmailResponseObject, error) {
	return nil, errNotImplemented
}

// GetAuthMethodsUsage — GET /v1/identity/auth-methods/usage: сколько игроков входят каждым способом.
func (*Handler) GetAuthMethodsUsage(context.Context, oapi.GetAuthMethodsUsageRequestObject) (oapi.GetAuthMethodsUsageResponseObject, error) {
	return nil, errNotImplemented
}
