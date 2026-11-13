package admin

import (
	"context"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
)

// ListGeoPlaces — GET /v1/geo/places: места GeoNames для активации (население по убыванию, geoname_id); q —
// начало названия на любом языке или латиницей, пустой после нормализации — 400 query.q minLength.
func (h *Handler) ListGeoPlaces(ctx context.Context, req oapi.ListGeoPlacesRequestObject) (oapi.ListGeoPlacesResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	p := geo.PlaceQuery{Limit: req.Params.Limit, Locale: locale}
	if req.Params.Q != nil {
		p.Q = *req.Params.Q
	}
	if req.Params.Country != nil {
		p.Country = *req.Params.Country
	}
	if req.Params.Cursor != nil {
		p.Cursor = *req.Params.Cursor
	}
	pg, err := r.Places(ctx, p)
	if err != nil {
		return nil, Problem(err)
	}
	body := oapi.GeoPlacePage{Items: make([]oapi.GeoPlace, 0, len(pg.Items)), NextCursor: next(pg.Next)}
	for _, pl := range pg.Items {
		body.Items = append(body.Items, oapi.GeoPlace{
			GeonameId: pl.GeonameID, Name: pl.Name, AsciiName: pl.ASCIIName, FeatureCode: pl.FeatureCode,
			Population: pl.Population, Timezone: pl.Timezone, RegionName: pl.RegionName,
			Country: oapi.GeoCountryRef{Code: pl.Country.Code, Name: pl.Country.Name}, CityId: pl.CityID,
		})
	}
	return oapi.ListGeoPlaces200JSONResponse{Body: body, Headers: oapi.ListGeoPlaces200ResponseHeaders{
		CacheControl: cachePrivate, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}
