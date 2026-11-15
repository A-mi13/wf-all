package httpapi

import (
	"context"
	"errors"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/auth"
)

// Операции области: вход, обновление токенов, выход — Task 11 плана I-2 (раскладка — Task 8, R5, Приложение 1).

// loginCacheNoStore — ответы с токенами (контракт: CacheNoStore).
const loginCacheNoStore = "no-store"

// errLoginNoPrincipal — операция с замком без принципала: конвейер (auth.Middleware + валидатор) до неё не пустит.
var errLoginNoPrincipal = errors.New("identity: нет принципала у операции с замком")

// Login — POST /v1/auth/login: вход по почте и паролю.
func (h Handler) Login(ctx context.Context, req oapi.LoginRequestObject) (oapi.LoginResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	out, err := h.app.Login(ctx, req.Body.Email, req.Body.Password, ClientFrom(ctx, req.Body.Device))
	if err != nil {
		return nil, err
	}
	body := authResult(out, false)
	return oapi.Login200JSONResponse{Body: body, Headers: oapi.Login200ResponseHeaders{CacheControl: loginCacheNoStore}}, nil
}

// Logout — POST /v1/auth/logout: выйти (текущая сессия принципала; уже отозванная — 204).
func (h Handler) Logout(ctx context.Context, _ oapi.LogoutRequestObject) (oapi.LogoutResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, ok := auth.From(ctx)
	if !ok {
		return nil, errLoginNoPrincipal
	}
	if err := h.app.Logout(ctx, p.SessionID); err != nil {
		return nil, err
	}
	return oapi.Logout204Response{}, nil
}

// RefreshSession — POST /v1/auth/refresh: обновить токены (вид клиента и метка BFF — из peer, install_id — из тела).
func (h Handler) RefreshSession(ctx context.Context, req oapi.RefreshSessionRequestObject) (oapi.RefreshSessionResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	install := ""
	if req.Body.InstallId != nil {
		install = *req.Body.InstallId
	}
	pair, err := h.app.Refresh(ctx, req.Body.RefreshToken, install, ClientFrom(ctx, nil))
	if err != nil {
		return nil, err
	}
	return oapi.RefreshSession200JSONResponse{Body: tokenPair(pair),
		Headers: oapi.RefreshSession200ResponseHeaders{CacheControl: loginCacheNoStore}}, nil
}
