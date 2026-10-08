// Package auth — кто делает запрос (спека бэкенда §6.2, §6.3): Principal из сессии в ctx,
// загрузка сессии с кэшем, слой аутентификации публичного API. Права на ресурс проверяет
// app/ модуля по Principal, а не хендлер.
package auth

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// Role — роль игрока из role_assignments (§4.5): после миграции 0020 — только captain со скоупом
// city. Роли сотрудников — не здесь: staff_roles модуля staff, в запросе — auth.Staff.
type Role struct {
	Name      string    // captain
	ScopeType string    // city
	ScopeID   uuid.UUID // город скоупа
}

// Principal — пользователь запроса. Загружается одним запросом по sid (identity) и не
// меняется: кэш отдаёт один указатель всем запросам сессии. Срезы Roles и Restrictions
// общие для всех запросов сессии: не изменять (append, sort, запись по индексу).
type Principal struct {
	UserID       uuid.UUID
	SessionID    uuid.UUID
	DeviceID     uuid.UUID // uuid.Nil — сессия без устройства
	Status       string    // user_status: active, suspended
	Restrictions []string  // закрытые санкцией возможности: create_match, comment, captain…
	Roles        []Role
}

// HasRole — роль с точным скоупом: глобальная роль не подразумевает городскую.
func (p *Principal) HasRole(name, scopeType string, scopeID uuid.UUID) bool {
	return slices.Contains(p.Roles, Role{Name: name, ScopeType: scopeType, ScopeID: scopeID})
}

// Restricted — возможность закрыта санкцией (403 identity.restricted решает модуль).
func (p *Principal) Restricted(capability string) bool {
	return slices.Contains(p.Restrictions, capability)
}

type principalKey struct{}

// With — ctx с Principal запроса.
func With(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// From — Principal запроса; false — запрос анонимный.
func From(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}

// Authenticated — для httpx.ValidateOptions: вход нужен операциям с security контракта.
func Authenticated(ctx context.Context) bool {
	_, ok := From(ctx)
	return ok
}
