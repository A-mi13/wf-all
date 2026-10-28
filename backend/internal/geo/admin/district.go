package admin

import (
	"context"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
)

// CreateGeoDistrict — POST /v1/geo/cities/{cityId}/districts: новый район города. Право geo.districts.manage в
// городе пути проверил конвейер (модератору — только свои города).
func (h *Handler) CreateGeoDistrict(ctx context.Context, req oapi.CreateGeoDistrictRequestObject) (oapi.CreateGeoDistrictResponseObject, error) {
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
	d, err := a.CreateDistrict(ctx, s, req.CityId, req.Body.Name, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.CreateGeoDistrict200JSONResponse(districtOf(d)), nil
}

// UpdateGeoDistrict — PATCH /v1/geo/cities/{cityId}/districts/{districtId}: переименование и архив района. Район
// другого города, чем в пути, — 404 сценария (существование не раскрывается).
func (h *Handler) UpdateGeoDistrict(ctx context.Context, req oapi.UpdateGeoDistrictRequestObject) (oapi.UpdateGeoDistrictResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	var p app.DistrictPatch
	if req.Body != nil {
		p = app.DistrictPatch{Name: req.Body.Name, Archived: req.Body.Archived}
	}
	d, err := a.UpdateDistrict(ctx, s, req.CityId, req.DistrictId, p, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.UpdateGeoDistrict200JSONResponse(districtOf(d)), nil
}

// districtOf — район в схеме GeoDistrict (список, создание, правка).
func districtOf(d geo.AdminDistrict) oapi.GeoDistrict {
	return oapi.GeoDistrict{Id: d.ID, Name: d.Name, ArchivedAt: d.ArchivedAt, Version: d.Version}
}
