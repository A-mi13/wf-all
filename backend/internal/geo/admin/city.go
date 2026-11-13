package admin

import (
	"context"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
)

// UpdateGeoCity — PATCH /v1/geo/cities/{cityId}: статус, адрес и названия города (спека geo §5.2, R10). Конвейер
// для x-permission: handler пропустил по «хоть одно право не deny» и город пути не сверял — права по каждому полю и
// город модератора проверяет сценарий (Decide), до записи.
func (h *Handler) UpdateGeoCity(ctx context.Context, req oapi.UpdateGeoCityRequestObject) (oapi.UpdateGeoCityResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	var p app.CityPatch
	if b := req.Body; b != nil {
		if b.Status != nil {
			st := geo.Status(*b.Status)
			p.Status = &st
		}
		p.Slug = b.Slug
		if b.Names != nil {
			p.Names = map[string]*string(*b.Names)
		}
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	c, err := a.UpdateCity(ctx, s, req.CityId, p, locale, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.UpdateGeoCity200JSONResponse{Body: cityDetails(c),
		Headers: oapi.UpdateGeoCity200ResponseHeaders{Vary: varyLanguage, ContentLanguage: lang}}, nil
}

// LinkGeoCity — POST /v1/geo/cities/{cityId}/link: ручная привязка города к месту GeoNames из отчёта сверки (R14).
// Право geo.cities.manage проверил конвейер (модератору — deny).
func (h *Handler) LinkGeoCity(ctx context.Context, req oapi.LinkGeoCityRequestObject) (oapi.LinkGeoCityResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	if req.Body == nil {
		return nil, errNoBody
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	c, err := a.LinkCity(ctx, s, req.CityId, req.Body.GeonameId, locale, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.LinkGeoCity200JSONResponse{Body: cityDetails(c),
		Headers: oapi.LinkGeoCity200ResponseHeaders{Vary: varyLanguage, ContentLanguage: lang}}, nil
}
