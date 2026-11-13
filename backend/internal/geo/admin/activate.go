package admin

import (
	"context"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/peer"
)

// ActivateGeoCity — POST /v1/geo/cities: город из места GeoNames (спека geo §5.3, R13) под Idempotency-Key
// конвейера (одна транзакция сценария). Право geo.cities.activate проверил конвейер (модератору — deny).
func (h *Handler) ActivateGeoCity(ctx context.Context, req oapi.ActivateGeoCityRequestObject) (oapi.ActivateGeoCityResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	in := app.Activation{GeonameID: req.Body.GeonameId, RegionID: req.Body.RegionId, Locale: locale}
	if req.Body.Status != nil {
		in.Status = geo.Status(*req.Body.Status)
	}
	if req.Body.CountryCode != nil {
		in.CountryCode = *req.Body.CountryCode
	}
	c, err := a.Activate(ctx, s, in, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.ActivateGeoCity200JSONResponse{Body: cityDetails(c),
		Headers: oapi.ActivateGeoCity200ResponseHeaders{Vary: varyLanguage, ContentLanguage: lang}}, nil
}

// meta — откуда запрос для аудита (R20): IP и User-Agent (≤ 512 байт) кладёт в ctx requestMeta сборки
// (approvals.WithMeta; TestGeoOperationsCarryMeta); без него (хендлер вне сборки admin) — IP из peer, UA пуст.
func meta(ctx context.Context) approvals.Meta {
	m := approvals.MetaFrom(ctx)
	if !m.IP.IsValid() {
		m.IP = peer.From(ctx).IP
	}
	return m
}
