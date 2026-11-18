package httpapi

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/page"
)

// Операции области: капитанство игрока — план I-4, Task 3 (спека §3.12). Нулевой Handler — 501.

// captaincyCachePrivate — Cache-Control ответов капитанства (CachePrivate контракта).
const captaincyCachePrivate = "private, no-store"

// captainApplication — app.CaptainApplication в схему CaptainApplication: телефон не возвращается (has_phone).
func captainApplication(v app.CaptainApplication) oapi.CaptainApplication {
	out := oapi.CaptainApplication{Id: v.ID, CityId: v.CityID, Status: oapi.CaptainApplicationStatus(v.Status),
		ProposedFormat: v.ProposedFormat, ProposedPitch: v.ProposedPitch, PlayFrequency: v.PlayFrequency,
		ContactLink: v.ContactLink, Note: v.Note, Comment: v.Comment, HasPhone: v.HasPhone, CreatedAt: v.CreatedAt,
		DecidedAt: v.DecidedAt}
	if v.RejectionCode != nil {
		c := oapi.CaptainRejectionCode(*v.RejectionCode)
		out.RejectionCode = &c
	}
	return out
}

// GetMyCaptaincy — GET /v1/me/captaincy: моё капитанство.
func (h Handler) GetMyCaptaincy(ctx context.Context, _ oapi.GetMyCaptaincyRequestObject) (oapi.GetMyCaptaincyResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.MyCaptaincy(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	body := oapi.Captaincy{Permits: make([]oapi.CaptainPermit, 0, len(v.Permits)),
		Eligibility: oapi.CaptainEligibility{CityId: v.CityID, Eligible: v.Eligible, AvailableAt: v.AvailableAt,
			Reasons: make([]oapi.CaptainIneligibility, 0, len(v.Reasons))}}
	for _, r := range v.Permits {
		body.Permits = append(body.Permits, oapi.CaptainPermit{CityId: r.CityID, GrantedAt: r.GrantedAt,
			Probationary: r.Probationary})
	}
	for _, r := range v.Reasons {
		body.Eligibility.Reasons = append(body.Eligibility.Reasons, oapi.CaptainIneligibility(r))
	}
	if v.OpenApplication != nil {
		open := captainApplication(*v.OpenApplication)
		body.OpenApplication = &open
	}
	return oapi.GetMyCaptaincy200JSONResponse{Body: body,
		Headers: oapi.GetMyCaptaincy200ResponseHeaders{CacheControl: captaincyCachePrivate}}, nil
}

// ListMyCaptainApplications — GET /v1/me/captain-applications: мои заявки в капитаны.
func (h Handler) ListMyCaptainApplications(ctx context.Context, req oapi.ListMyCaptainApplicationsRequestObject) (oapi.ListMyCaptainApplicationsResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	list, next, err := h.app.ListMyCaptainApplications(ctx, p.UserID, cursor, page.Limit(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	body := oapi.CaptainApplicationPage{Items: make([]oapi.CaptainApplication, 0, len(list))}
	for _, v := range list {
		body.Items = append(body.Items, captainApplication(v))
	}
	if next != "" {
		body.NextCursor = &next
	}
	return oapi.ListMyCaptainApplications200JSONResponse{Body: body,
		Headers: oapi.ListMyCaptainApplications200ResponseHeaders{CacheControl: captaincyCachePrivate}}, nil
}

// SubmitCaptainApplication — POST /v1/captain-applications: подать заявку в капитаны.
func (h Handler) SubmitCaptainApplication(ctx context.Context, req oapi.SubmitCaptainApplicationRequestObject) (oapi.SubmitCaptainApplicationResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var in app.CaptainApplicationInput
	if b := req.Body; b != nil {
		in = app.CaptainApplicationInput{ProposedFormat: b.ProposedFormat, ProposedPitch: b.ProposedPitch,
			PlayFrequency: b.PlayFrequency, ContactLink: b.ContactLink, Note: b.Note, Phone: b.Phone}
	}
	v, err := h.app.SubmitCaptainApplication(ctx, p.UserID, in)
	if err != nil {
		return nil, err
	}
	return oapi.SubmitCaptainApplication201JSONResponse{Body: captainApplication(v),
		Headers: oapi.SubmitCaptainApplication201ResponseHeaders{CacheControl: captaincyCachePrivate}}, nil
}

// WithdrawCaptainApplication — POST /v1/captain-applications/{applicationId}/withdraw: отозвать заявку в капитаны.
func (h Handler) WithdrawCaptainApplication(ctx context.Context, req oapi.WithdrawCaptainApplicationRequestObject) (oapi.WithdrawCaptainApplicationResponseObject, error) {
	if h.app == nil {
		return nil, errNotImplemented
	}
	p, err := accountPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.app.WithdrawCaptainApplication(ctx, p.UserID, req.ApplicationId)
	if err != nil {
		return nil, err
	}
	return oapi.WithdrawCaptainApplication200JSONResponse{Body: captainApplication(v),
		Headers: oapi.WithdrawCaptainApplication200ResponseHeaders{CacheControl: captaincyCachePrivate}}, nil
}
