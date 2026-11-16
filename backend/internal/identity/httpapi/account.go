package httpapi

import (
	"context"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
)

// Операции области: мой аккаунт, ник, документы, мои способы входа — Task 14 плана I-2 (раскладка — Task 8, R5,
// Приложение 1). Нулевой Handler — 501 (TestZeroHandlerNotImplemented).

// accountCachePrivate — Cache-Control ответов, зависящих от игрока (CachePrivate контракта).
const accountCachePrivate = "private, no-store"

// errAccountUnauthenticated — операции с замком без принципала (конвейер такое не пропускает — защита в глубину).
var errAccountUnauthenticated = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)

// accountPrincipal — принципал операции с замком.
func accountPrincipal(ctx context.Context) (*auth.Principal, error) {
	p, ok := auth.From(ctx)
	if !ok || p == nil {
		return nil, errAccountUnauthenticated
	}
	return p, nil
}

// AccountMe — app.MeView в схему Me (её же отдают confirmEmailChange и другие операции «→ 200 Me»).
func AccountMe(v app.MeView) oapi.Me {
	m := oapi.Me{Id: v.ID, Email: v.Email, Nickname: v.Nickname, Locale: oapi.Locale(v.Locale),
		Status: oapi.AccountStatus(v.Status), CityId: v.CityID, CreatedAt: v.CreatedAt,
		BirthDate: openapi_types.Date{Time: v.BirthDate}, NicknameChangeableAt: v.NicknameChangeableAt,
		HasPassword: v.HasPassword, MfaEnabled: v.MfaEnabled, DocumentsAcceptanceRequired: v.DocumentsAcceptanceRequired,
		Restrictions: make([]oapi.MeRestriction, 0, len(v.Restrictions)), Documents: make([]oapi.MeDocument, 0, len(v.Documents))}
	for _, r := range v.Restrictions {
		m.Restrictions = append(m.Restrictions, oapi.MeRestriction{Capability: r.Capability, Until: r.Until})
	}
	for _, d := range v.Documents {
		m.Documents = append(m.Documents, oapi.MeDocument{Document: oapi.LegalDocumentKind(d.Document),
			AcceptedVersion: d.AcceptedVersion, CurrentVersion: d.CurrentVersion, Url: d.URL})
	}
	return m
}

// AcceptDocuments — POST /v1/me/documents: принять документы.
func (h Handler) AcceptDocuments(ctx context.Context, req oapi.AcceptDocumentsRequestObject) (oapi.AcceptDocumentsResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	docs := make([]domain.Accepted, 0, len(req.Body.AcceptedDocuments))
	for _, d := range req.Body.AcceptedDocuments {
		docs = append(docs, domain.Accepted{Document: domain.Document(d.Document), Version: d.Version})
	}
	v, err := h.app.AcceptDocuments(ctx, p.UserID, docs, ClientFrom(ctx, nil).Kind)
	if err != nil {
		return nil, err
	}
	return oapi.AcceptDocuments200JSONResponse{Body: AccountMe(v),
		Headers: oapi.AcceptDocuments200ResponseHeaders{CacheControl: accountCachePrivate}}, nil
}

// ChangeNickname — PATCH /v1/me/nickname: сменить ник.
func (h Handler) ChangeNickname(ctx context.Context, req oapi.ChangeNicknameRequestObject) (oapi.ChangeNicknameResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.ChangeNickname(ctx, p.UserID, req.Body.Nickname)
	if err != nil {
		return nil, err
	}
	return oapi.ChangeNickname200JSONResponse{Body: AccountMe(v),
		Headers: oapi.ChangeNickname200ResponseHeaders{CacheControl: accountCachePrivate}}, nil
}

// GetMe — GET /v1/me: мой аккаунт.
func (h Handler) GetMe(ctx context.Context, _ oapi.GetMeRequestObject) (oapi.GetMeResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.Me(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return oapi.GetMe200JSONResponse{Body: AccountMe(v), Headers: oapi.GetMe200ResponseHeaders{CacheControl: accountCachePrivate}}, nil
}

// ListMyAuthMethods — GET /v1/me/auth-methods: мои способы входа.
func (h Handler) ListMyAuthMethods(ctx context.Context, _ oapi.ListMyAuthMethodsRequestObject) (oapi.ListMyAuthMethodsResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.MyAuthMethods(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	body := oapi.MyAuthMethods{HasPassword: v.HasPassword, Mfa: AccountMfaStatus(v.Mfa),
		Identities: make([]oapi.LinkedIdentity, 0, len(v.Identities)), Passkeys: make([]oapi.Passkey, 0, len(v.Passkeys))}
	for _, i := range v.Identities {
		body.Identities = append(body.Identities, oapi.LinkedIdentity{Id: i.ID, Provider: oapi.SocialProvider(i.Provider),
			Email: i.Email, EmailIsPrivateRelay: i.EmailIsPrivateRelay, LinkedAt: i.LinkedAt})
	}
	for _, k := range v.Passkeys {
		body.Passkeys = append(body.Passkeys, oapi.Passkey{Id: k.ID, Name: k.Name, CreatedAt: k.CreatedAt,
			LastUsedAt: k.LastUsedAt, BackedUp: k.BackedUp})
	}
	return oapi.ListMyAuthMethods200JSONResponse{Body: body,
		Headers: oapi.ListMyAuthMethods200ResponseHeaders{CacheControl: accountCachePrivate}}, nil
}

// AccountMfaStatus — app.MfaStatusView в схему MfaStatus (её же отдаёт getMyMfa, Task 16).
func AccountMfaStatus(v app.MfaStatusView) oapi.MfaStatus {
	return oapi.MfaStatus{TotpEnabled: v.TOTPEnabled, ConfirmedAt: v.ConfirmedAt, RecoveryCodesLeft: v.RecoveryCodesLeft,
		ResetScheduledAt: v.ResetScheduledAt}
}

// UpdateMe — PATCH /v1/me: настройки аккаунта.
func (h Handler) UpdateMe(ctx context.Context, req oapi.UpdateMeRequestObject) (oapi.UpdateMeResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body.Locale == nil {
		return nil, httpx.NewFieldError("body", "minProperties")
	}
	v, err := h.app.UpdateMe(ctx, p.UserID, string(*req.Body.Locale))
	if err != nil {
		return nil, err
	}
	return oapi.UpdateMe200JSONResponse{Body: AccountMe(v), Headers: oapi.UpdateMe200ResponseHeaders{CacheControl: accountCachePrivate}}, nil
}
