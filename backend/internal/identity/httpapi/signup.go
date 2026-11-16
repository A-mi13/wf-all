package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/identity/internal/domain"
)

// Операции области: регистрация по почте, коды из письма, ник, документы — владелец Task 10 плана I-2 (спека
// identity §3.1, §3.14, §3.16, §5.8). Нулевой Handler (без App) — 501, как у каждой операции каркаса.

// Cache-Control ответов области (контракт: CachePrivate, CacheNoStore, public у документов).
const (
	codeCachePrivate = "private, no-store"
	codeCacheNoStore = "no-store"
	cacheLegalDocs   = "public, max-age=300"
)

// codeSent — CodeSent контракта.
func codeSent(s app.CodeSent) oapi.CodeSent {
	return oapi.CodeSent{CodeId: s.CodeID, ExpiresAt: s.ExpiresAt, ResendAvailableAt: s.ResendAvailable}
}

// CheckNickname — GET /v1/nicknames/availability: доступность ника.
func (h Handler) CheckNickname(ctx context.Context, req oapi.CheckNicknameRequestObject) (oapi.CheckNicknameResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	ok, reason, err := h.app.CheckNickname(ctx, req.Params.Nickname)
	if err != nil {
		return nil, err
	}
	body := oapi.NicknameAvailability{Available: ok}
	if !ok {
		r := oapi.NicknameUnavailableReason(reason)
		body.Reason = &r
	}
	return oapi.CheckNickname200JSONResponse{Body: body, Headers: oapi.CheckNickname200ResponseHeaders{CacheControl: codeCachePrivate}}, nil
}

// StartSignup — POST /v1/auth/signup: начать регистрацию по почте.
func (h Handler) StartSignup(ctx context.Context, req oapi.StartSignupRequestObject) (oapi.StartSignupResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	b := req.Body
	in := app.SignupInput{Email: b.Email, Password: b.Password, Nickname: b.Nickname, BirthDate: b.BirthDate.Time,
		CityID: b.CityId}
	if b.Locale != nil {
		in.Locale = string(*b.Locale)
	}
	for _, d := range b.AcceptedDocuments {
		in.Accepted = append(in.Accepted, domain.Accepted{Document: domain.Document(d.Document), Version: d.Version})
	}
	sent, err := h.app.StartSignup(ctx, in, ClientFrom(ctx, b.Device))
	if err != nil {
		return nil, err
	}
	return oapi.StartSignup202JSONResponse{Body: codeSent(sent), Headers: oapi.StartSignup202ResponseHeaders{CacheControl: codeCachePrivate}}, nil
}

// ConfirmSignup — POST /v1/auth/signup/confirm: подтвердить регистрацию кодом.
func (h Handler) ConfirmSignup(ctx context.Context, req oapi.ConfirmSignupRequestObject) (oapi.ConfirmSignupResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	b := req.Body
	in := app.ConfirmInput{CodeID: b.CodeId, Code: b.Code}
	if b.Nickname != nil {
		in.Nickname = *b.Nickname
	}
	out, err := h.app.ConfirmSignup(ctx, in, ClientFrom(ctx, b.Device))
	if err != nil {
		return nil, err
	}
	return oapi.ConfirmSignup201JSONResponse{Body: authResult(out, true),
		Headers: oapi.ConfirmSignup201ResponseHeaders{CacheControl: codeCacheNoStore}}, nil
}

// GetLegalDocuments — GET /v1/legal/documents: текущие версии документов.
func (h Handler) GetLegalDocuments(context.Context, oapi.GetLegalDocumentsRequestObject) (oapi.GetLegalDocumentsResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	docs := h.app.LegalDocuments()
	items := make([]oapi.LegalDocument, len(docs))
	for i, d := range docs {
		items[i] = oapi.LegalDocument{Document: oapi.LegalDocumentKind(d.Document), Version: d.Version, Url: d.URL}
	}
	return oapi.GetLegalDocuments200JSONResponse{Body: oapi.LegalDocumentList{Items: items},
		Headers: oapi.GetLegalDocuments200ResponseHeaders{CacheControl: cacheLegalDocs}}, nil
}

// ResendAuthCode — POST /v1/auth/codes/{codeId}/resend: отправить код повторно.
func (h Handler) ResendAuthCode(ctx context.Context, req oapi.ResendAuthCodeRequestObject) (oapi.ResendAuthCodeResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	sent, err := h.app.ResendCode(ctx, req.CodeId)
	if err != nil {
		return nil, err
	}
	return oapi.ResendAuthCode202JSONResponse{Body: codeSent(sent), Headers: oapi.ResendAuthCode202ResponseHeaders{CacheControl: codeCachePrivate}}, nil
}
