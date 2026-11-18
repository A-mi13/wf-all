package admin

import (
	"context"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/page"
)

// Заявки в капитаны (спека identity §3.12 п. 6, §7.1; план I-4, Task 4): очередь, карточка, контакты заявителя,
// одобрение и отказ — тонкие ручки: сотрудник и заголовок причины → сценарий app → ответ контракта. Скоуп
// модератора, перечтение связи сотрудника и права в момент решения — в сценарии. Чтения — без кэша (CachePrivate);
// причина доступа — только в audit_log.reason.

// captainsCachePrivate — ответы с данными заявителя не кэшируются (контракт: CachePrivate).
const captainsCachePrivate = "private, no-store"

func captainApplication(v app.AdminApplication) oapi.IdentityCaptainApplication {
	out := oapi.IdentityCaptainApplication{Id: v.ID, UserId: v.UserID, Nickname: v.Nickname, CityId: v.CityID,
		Status: oapi.IdentityCaptainApplicationStatus(v.Status), AgeYears: v.AgeYears, AccountAgeDays: v.AccountAgeDays,
		ProposedFormat: v.ProposedFormat, ProposedPitch: v.ProposedPitch, PlayFrequency: v.PlayFrequency,
		ContactLink: v.ContactLink, Note: v.Note, HasPhone: v.HasPhone, CreatedAt: v.CreatedAt, DecidedAt: v.DecidedAt,
		DecidedByLabel: v.DecidedByLabel, Comment: v.Comment}
	if v.RejectionCode != nil {
		c := oapi.IdentityCaptainRejectionCode(*v.RejectionCode)
		out.RejectionCode = &c
	}
	return out
}

func staffOf(ctx context.Context) (*auth.Staff, error) {
	st, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	return st, nil
}

// ListCaptainApplications — GET /v1/identity/captain-applications: очередь заявок в капитаны.
func (h *Handler) ListCaptainApplications(ctx context.Context, req oapi.ListCaptainApplicationsRequestObject) (oapi.ListCaptainApplicationsResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	st, err := staffOf(ctx)
	if err != nil {
		return nil, err
	}
	f := app.CaptainQueueFilter{Limit: page.Limit(req.Params.Limit)}
	if req.Params.Status != nil {
		f.Status = string(*req.Params.Status)
	}
	if req.Params.CityId != nil {
		f.CityID = *req.Params.CityId
	}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	list, next, err := h.app.ListCaptainApplications(ctx, st, f)
	if err != nil {
		return nil, err
	}
	body := oapi.IdentityCaptainApplicationPage{Items: make([]oapi.IdentityCaptainApplication, 0, len(list))}
	for _, v := range list {
		body.Items = append(body.Items, captainApplication(v))
	}
	if next != "" {
		body.NextCursor = &next
	}
	return oapi.ListCaptainApplications200JSONResponse{Body: body,
		Headers: oapi.ListCaptainApplications200ResponseHeaders{CacheControl: captainsCachePrivate}}, nil
}

// GetCaptainApplication — GET /v1/identity/captain-applications/{applicationId}: карточка заявки.
func (h *Handler) GetCaptainApplication(ctx context.Context, req oapi.GetCaptainApplicationRequestObject) (oapi.GetCaptainApplicationResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	st, err := staffOf(ctx)
	if err != nil {
		return nil, err
	}
	card, err := h.app.CaptainApplicationCard(ctx, st, req.ApplicationId)
	if err != nil {
		return nil, err
	}
	var body oapi.IdentityCaptainApplicationCard
	body.Application = captainApplication(card.Application)
	a := &body.Applicant
	a.UserId, a.Nickname, a.CityId = card.Applicant.UserID, card.Applicant.Nickname, card.Applicant.CityID
	a.AgeYears, a.AccountAgeDays = card.Applicant.AgeYears, card.Applicant.AccountAgeDays
	body.Checks = make([]struct {
		Check oapi.IdentityCaptainCheck `json:"check"`
		State oapi.IdentityCheckState   `json:"state"`
	}, len(card.Checks))
	for i, c := range card.Checks {
		body.Checks[i].Check, body.Checks[i].State = oapi.IdentityCaptainCheck(c.Check), oapi.IdentityCheckState(c.State)
	}
	body.History = make([]oapi.IdentityCaptainApplication, 0, len(card.History))
	for _, v := range card.History {
		body.History = append(body.History, captainApplication(v))
	}
	body.LinkedAccounts = card.LinkedAccounts
	return oapi.GetCaptainApplication200JSONResponse{Body: body,
		Headers: oapi.GetCaptainApplication200ResponseHeaders{CacheControl: captainsCachePrivate}}, nil
}

// GetCaptainApplicationContact — GET /v1/identity/captain-applications/{applicationId}/contact: контакты заявителя.
func (h *Handler) GetCaptainApplicationContact(ctx context.Context, req oapi.GetCaptainApplicationContactRequestObject) (oapi.GetCaptainApplicationContactResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	st, err := staffOf(ctx)
	if err != nil {
		return nil, err
	}
	reason, err := decodeAccessReason(req.Params.XWFAccessReason)
	if err != nil {
		return nil, err
	}
	email, phone, err := h.app.CaptainApplicantContact(ctx, st, req.ApplicationId, reason)
	if err != nil {
		return nil, err
	}
	return oapi.GetCaptainApplicationContact200JSONResponse{Body: oapi.IdentityApplicantContact{Email: email, Phone: phone},
		Headers: oapi.GetCaptainApplicationContact200ResponseHeaders{CacheControl: captainsCachePrivate}}, nil
}

// ApproveCaptainApplication — POST /v1/identity/captain-applications/{applicationId}/approve: одобрить заявку.
func (h *Handler) ApproveCaptainApplication(ctx context.Context, req oapi.ApproveCaptainApplicationRequestObject) (oapi.ApproveCaptainApplicationResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	st, err := staffOf(ctx)
	if err != nil {
		return nil, err
	}
	var comment *string
	if req.Body != nil {
		comment = req.Body.Comment
	}
	v, err := h.app.ApproveCaptainApplication(ctx, st, req.ApplicationId, comment)
	if err != nil {
		return nil, err
	}
	return oapi.ApproveCaptainApplication200JSONResponse(captainApplication(v)), nil
}

// RejectCaptainApplication — POST /v1/identity/captain-applications/{applicationId}/reject: отклонить заявку.
func (h *Handler) RejectCaptainApplication(ctx context.Context, req oapi.RejectCaptainApplicationRequestObject) (oapi.RejectCaptainApplicationResponseObject, error) {
	if h == nil || h.app == nil {
		return nil, errNotImplemented
	}
	st, err := staffOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errNoBody
	}
	v, err := h.app.RejectCaptainApplication(ctx, st, req.ApplicationId, string(req.Body.RejectionCode), req.Body.Comment)
	if err != nil {
		return nil, err
	}
	return oapi.RejectCaptainApplication200JSONResponse(captainApplication(v)), nil
}
