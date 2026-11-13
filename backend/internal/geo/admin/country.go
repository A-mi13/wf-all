package admin

import (
	"context"

	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
)

// RegisterApprovals — действия geo «вдвоём» (geo.country_rules, geo.country_create, R25) в реестре подтверждений и
// сервис для запросов из сценариев. Сборка admin-api зовёт до reg.Validate() (Task 16); нулевой Handler (без пула
// или часов) ничего не регистрирует — его операции стран отвечают 501.
func (h *Handler) RegisterApprovals(r *approvals.Registry, svc *approvals.Service) {
	if h == nil || h.admin == nil {
		return
	}
	h.admin.RegisterApprovals(r, svc)
}

// CreateGeoCountry — POST /v1/geo/countries: новая страна только через подтверждение вдвоём — всегда 202 с запросом
// geo.country_create (R25). Право geo.countries.rules (dual) проверил конвейер.
func (h *Handler) CreateGeoCountry(ctx context.Context, req oapi.CreateGeoCountryRequestObject) (oapi.CreateGeoCountryResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	b := req.Body
	if b == nil {
		return nil, errNoBody
	}
	in := app.CountryInput{
		CountryRules: domain.CountryRules{Code: b.Code, Currency: b.Currency, DefaultLocale: b.DefaultLocale,
			PhonePrefix: b.PhonePrefix, WeekStartsOn: b.WeekStartsOn, MinSignupAge: b.MinSignupAge, AgeOfMajority: b.AgeOfMajority},
		Names: map[string]string(b.Names),
	}
	if b.IsEnabled != nil {
		in.IsEnabled = *b.IsEnabled
	}
	ap, err := a.CreateCountry(ctx, s, in, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	out, err := oapi.ApprovalOf(ap)
	if err != nil {
		return nil, err
	}
	return oapi.CreateGeoCountry202JSONResponse{Approval: out}, nil
}

// UpdateGeoCountry — PATCH /v1/geo/countries/{code}: смена валюты, порогов и выключение — 202 с запросом
// geo.country_rules (всегда вдвоём, решение 27.10); прочее и включение — 200 сразу. Конвейер для handler пропустил
// «хоть одно право не deny», точное право решает сценарий.
func (h *Handler) UpdateGeoCountry(ctx context.Context, req oapi.UpdateGeoCountryRequestObject) (oapi.UpdateGeoCountryResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	var p app.CountryPatch
	if b := req.Body; b != nil {
		p = app.CountryPatch{Currency: b.Currency, PhonePrefix: b.PhonePrefix, WeekStartsOn: b.WeekStartsOn,
			MinSignupAge: b.MinSignupAge, AgeOfMajority: b.AgeOfMajority, IsEnabled: b.IsEnabled}
		if b.Names != nil {
			p.Names = map[string]*string(*b.Names)
		}
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	c, ap, err := a.UpdateCountry(ctx, s, req.Code, p, locale, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	if ap != nil {
		out, err := oapi.ApprovalOf(*ap)
		if err != nil {
			return nil, err
		}
		return oapi.UpdateGeoCountry202JSONResponse{Approval: out}, nil
	}
	return oapi.UpdateGeoCountry200JSONResponse{Body: countryOf(c),
		Headers: oapi.UpdateGeoCountry200ResponseHeaders{Vary: varyLanguage, ContentLanguage: lang}}, nil
}
