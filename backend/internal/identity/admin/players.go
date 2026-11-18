package admin

import (
	"context"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
)

// Поиск игрока по почте для поддержки и статистика способов входа (спека identity §3.15 п. 2, §7.1, §4.3; план I-4,
// Task 5): тонкие ручки — заголовки ПД (headers.go) → сценарий app → ответ контракта. Почта и причина — только в
// памяти запроса: в журнал запросов заголовки не пишутся (httpx.accessLog — метод, путь, статус), в audit_log — только
// причина (R10).

// playersCachePrivate — ответы с ПД не кэшируются (контракт: CachePrivate).
const playersCachePrivate = "private, no-store"

// FindPlayerByEmail — GET /v1/identity/users/lookup: найти игрока по почте.
func (h *Handler) FindPlayerByEmail(ctx context.Context, req oapi.FindPlayerByEmailRequestObject) (oapi.FindPlayerByEmailResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	st, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	email, err := decodeLookupEmail(req.Params.XWFLookupEmail)
	if err != nil {
		return nil, err
	}
	reason, err := decodeAccessReason(req.Params.XWFAccessReason)
	if err != nil {
		return nil, err
	}
	p, err := h.app.FindPlayerByEmail(ctx, st, email, reason)
	if err != nil {
		return nil, err
	}
	body := oapi.IdentityPlayerLookup{Id: p.ID, Nickname: p.Nickname, CityId: p.CityID, Status: p.Status,
		CreatedAt: p.CreatedAt.UTC(), MfaEnabled: p.MfaEnabled}
	if p.MfaReset != nil {
		r := alloc(&body.MfaReset) // анонимный тип поля генератора: свой литерал повторял бы его теги
		r.Id, r.ExecuteAt = p.MfaReset.ID, p.MfaReset.ExecuteAt.UTC()
	}
	return oapi.FindPlayerByEmail200JSONResponse{Body: body,
		Headers: oapi.FindPlayerByEmail200ResponseHeaders{CacheControl: playersCachePrivate}}, nil
}

// GetAuthMethodsUsage — GET /v1/identity/auth-methods/usage: сколько игроков входят каждым способом.
func (h *Handler) GetAuthMethodsUsage(ctx context.Context, _ oapi.GetAuthMethodsUsageRequestObject) (oapi.GetAuthMethodsUsageResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	rows, err := h.app.AuthMethodsUsage(ctx)
	if err != nil {
		return nil, err
	}
	var body oapi.IdentityAuthMethodsUsage
	body.Items = make([]struct {
		Linked     int                         `json:"linked"`
		Method     oapi.IdentityAuthMethodKind `json:"method"`
		OnlyMethod int                         `json:"only_method"`
	}, 0, len(rows))
	for _, r := range rows {
		body.Items = append(body.Items, struct {
			Linked     int                         `json:"linked"`
			Method     oapi.IdentityAuthMethodKind `json:"method"`
			OnlyMethod int                         `json:"only_method"`
		}{Linked: int(r.Linked), Method: oapi.IdentityAuthMethodKind(r.Method), OnlyMethod: int(r.OnlyMethod)})
	}
	return oapi.GetAuthMethodsUsage200JSONResponse{Body: body,
		Headers: oapi.GetAuthMethodsUsage200ResponseHeaders{CacheControl: playersCachePrivate}}, nil
}

// alloc — новое значение под указателем *p (поле анонимного типа сгенерированной схемы).
func alloc[T any](p **T) *T {
	*p = new(T)
	return *p
}
