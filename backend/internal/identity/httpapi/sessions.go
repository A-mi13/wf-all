package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/auth"
)

// Операции области: «Где вы вошли» и способы входа — Task 12 плана I-2 (раскладка — Task 8, R5, Приложение 1).

const (
	// sessionsCachePrivate — ответы аккаунта (контракт: CachePrivate)
	sessionsCachePrivate = "private, no-store"
	// sessionsCacheAuthMethods — способы входа одинаковы для всех: общие кэши хранят до минуты (§4.4)
	sessionsCacheAuthMethods = "public, max-age=60"
)

// GetAuthMethods — GET /v1/auth/methods: способы входа.
func (h Handler) GetAuthMethods(ctx context.Context, _ oapi.GetAuthMethodsRequestObject) (oapi.GetAuthMethodsResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	v, err := h.app.AuthMethods(ctx)
	if err != nil {
		return nil, err
	}
	return oapi.GetAuthMethods200JSONResponse{Body: oapi.AuthMethods{
		Password: v.Password,
		Apple:    oapi.AuthMethodSwitch{Signup: v.AppleSignup, Login: v.AppleLogin},
		Google:   oapi.AuthMethodSwitch{Signup: v.GoogleSignup, Login: v.GoogleLogin},
		Passkeys: oapi.PasskeyMethodSwitch{Register: v.PasskeysRegister, Login: v.PasskeysLogin},
	}, Headers: oapi.GetAuthMethods200ResponseHeaders{CacheControl: sessionsCacheAuthMethods}}, nil
}

// ListMySessions — GET /v1/me/sessions: «Где вы вошли».
func (h Handler) ListMySessions(ctx context.Context, _ oapi.ListMySessionsRequestObject) (oapi.ListMySessionsResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, ok := auth.From(ctx)
	if !ok {
		return nil, errLoginNoPrincipal
	}
	list, err := h.app.ListSessions(ctx, p.UserID, p.SessionID)
	if err != nil {
		return nil, err
	}
	items := make([]oapi.Session, 0, len(list))
	for _, s := range list {
		items = append(items, sessionItem(s))
	}
	return oapi.ListMySessions200JSONResponse{Body: oapi.SessionList{Items: items},
		Headers: oapi.ListMySessions200ResponseHeaders{CacheControl: sessionsCachePrivate}}, nil
}

// sessionItem — строка «Где вы вошли»: пустое поле — null (у веба нет устройства и версии приложения, у приложения —
// браузера); браузер — «браузер, ОС».
func sessionItem(s app.SessionView) oapi.Session {
	it := oapi.Session{Id: s.ID, ClientKind: oapi.ClientKind(s.ClientKind), AuthMethod: oapi.AuthMethod(s.AuthMethod),
		City: sessionOptString(s.City), CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, Current: s.Current,
		DeviceModel: sessionOptString(s.Model), OsVersion: sessionOptString(s.OSVersion), AppVersion: sessionOptString(s.AppVersion)}
	if s.Platform != "" {
		pl := oapi.AppPlatform(s.Platform)
		it.Platform = &pl
	}
	switch {
	case s.Browser != "" && s.OS != "":
		b := s.Browser + ", " + s.OS
		it.Browser = &b
	case s.Browser != "":
		it.Browser = sessionOptString(s.Browser)
	case s.OS != "":
		it.Browser = sessionOptString(s.OS)
	}
	return it
}

func sessionOptString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// RevokeMySession — DELETE /v1/me/sessions/{sessionId}: выйти на устройстве.
func (h Handler) RevokeMySession(ctx context.Context, req oapi.RevokeMySessionRequestObject) (oapi.RevokeMySessionResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, ok := auth.From(ctx)
	if !ok {
		return nil, errLoginNoPrincipal
	}
	if err := h.app.RevokeSession(ctx, p.UserID, p.SessionID, req.SessionId); err != nil {
		return nil, err
	}
	return oapi.RevokeMySession204Response{}, nil
}

// RevokeOtherSessions — POST /v1/me/sessions/revoke-others: выйти везде, кроме этого устройства.
func (h Handler) RevokeOtherSessions(ctx context.Context, _ oapi.RevokeOtherSessionsRequestObject) (oapi.RevokeOtherSessionsResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, ok := auth.From(ctx)
	if !ok {
		return nil, errLoginNoPrincipal
	}
	n, err := h.app.RevokeOthers(ctx, p.UserID, p.SessionID)
	if err != nil {
		return nil, err
	}
	return oapi.RevokeOtherSessions200JSONResponse{Body: oapi.RevokedCount{Revoked: n},
		Headers: oapi.RevokeOtherSessions200ResponseHeaders{CacheControl: sessionsCachePrivate}}, nil
}
