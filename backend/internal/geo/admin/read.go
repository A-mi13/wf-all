package admin

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
)

// permCitiesRead — право чтения городов (perm, §2.5): модератору — scoped, только его города.
const permCitiesRead = "geo.cities.read"

// ListGeoCities — GET /v1/geo/cities: города всех статусов и стран, в том числе выключенных (country_enabled);
// фильтры status, country, region_id, q, ids складываются (P28). Модератору (право scoped) — только его города:
// ids пересекаются с ними, без ids — они сами; скоуп — по GrantScoped, а не по len(CityIDs): модератор без
// городов получает пустой список, не весь справочник (M10).
func (h *Handler) ListGeoCities(ctx context.Context, req oapi.ListGeoCitiesRequestObject) (oapi.ListGeoCitiesResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		// без сессии до хендлера не доходят (валидатор), это защита в глубину
		return nil, errUnauthenticated
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	p := geo.AdminCityQuery{Limit: req.Params.Limit, Locale: locale, RegionID: req.Params.RegionId}
	if req.Params.Status != nil {
		for _, st := range *req.Params.Status {
			p.Status = append(p.Status, geo.Status(st))
		}
	}
	if req.Params.Country != nil {
		p.Country = *req.Params.Country
	}
	if req.Params.Q != nil {
		p.Q = *req.Params.Q
	}
	if req.Params.Cursor != nil {
		p.Cursor = *req.Params.Cursor
	}
	if req.Params.Ids != nil {
		p.IDs = append(make([]uuid.UUID, 0, len(*req.Params.Ids)), *req.Params.Ids...)
	}
	switch s.Grant(permCitiesRead) {
	case auth.GrantDeny:
		return nil, errForbidden
	case auth.GrantScoped:
		p.IDs = scope(p.IDs, s.CityIDs)
	}
	pg, err := r.Cities(ctx, p)
	if err != nil {
		return nil, Problem(err) // в том числе page.ErrBadCursor — 400 по query.cursor
	}
	body := oapi.GeoCityPage{Items: make([]oapi.GeoCity, 0, len(pg.Items))}
	for _, c := range pg.Items {
		out := oapi.GeoCity{
			Id: c.ID, Slug: c.Slug, Name: c.Name, Status: oapi.CityStatus(c.Status), CountryCode: c.CountryCode,
			CountryEnabled: c.CountryEnabled, GeonameId: c.GeonameID, Population: c.Population,
			DistrictsCount: c.DistrictsCount,
		}
		if c.Region != nil {
			id, name := c.Region.ID, c.Region.Name
			out.RegionId, out.RegionName = &id, &name
		}
		body.Items = append(body.Items, out)
	}
	body.NextCursor = next(pg.Next)
	return oapi.ListGeoCities200JSONResponse{Body: body, Headers: oapi.ListGeoCities200ResponseHeaders{
		CacheControl: cachePrivate, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

// scope — города модератора как фильтр списка: без запрошенных — все его города, с запрошенными — их
// пересечение с его городами. Всегда не nil (пустой — ни одного города).
func scope(requested, own []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(own))
	for _, c := range own {
		if requested == nil || slices.Contains(requested, c) {
			out = append(out, c)
		}
	}
	return out
}

// next — курсор следующей страницы: "" — null.
func next(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// GetGeoCity — GET /v1/geo/cities/{cityId}: карточка города любой страны. Город модератора сверяет конвейер
// (geo.cities.read scoped с {cityId}: чужой — 403 до хендлера).
func (h *Handler) GetGeoCity(ctx context.Context, req oapi.GetGeoCityRequestObject) (oapi.GetGeoCityResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	c, err := r.City(ctx, req.CityId, locale)
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.GetGeoCity200JSONResponse{Body: cityDetails(c), Headers: oapi.GetGeoCity200ResponseHeaders{
		CacheControl: cachePrivate, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

// cityDetails — карточка города в схеме GeoCityDetails (чтение, активация, правка).
func cityDetails(c geo.AdminCity) oapi.GeoCityDetails {
	out := oapi.GeoCityDetails{
		Id: c.ID, Slug: c.Slug, SlugHistory: make([]oapi.GeoSlugHistoryEntry, 0, len(c.SlugHistory)), Name: c.Name,
		Names: names(c.Names), CanonicalLocale: c.CanonicalLocale, Status: oapi.CityStatus(c.Status),
		GeonameId: c.GeonameID, Population: c.Population, Timezone: c.Timezone,
		Location:       oapi.GeoPoint{Lat: c.Location.Lat, Lon: c.Location.Lon},
		Country:        oapi.GeoCityCountry{Code: c.Country.Code, Name: c.Country.Name, IsEnabled: c.Country.IsEnabled},
		DistrictsCount: c.DistrictsCount, Version: c.Version,
	}
	for _, e := range c.SlugHistory {
		out.SlugHistory = append(out.SlugHistory, oapi.GeoSlugHistoryEntry{Slug: e.Slug, ReplacedAt: e.ReplacedAt})
	}
	if c.Region != nil {
		out.Region = &oapi.GeoRegionRef{Id: c.Region.ID, Name: c.Region.Name}
	}
	return out
}

// names — названия по языкам; nil — пустой объект (поле обязательное).
func names(m map[string]string) oapi.GeoNames {
	out := make(oapi.GeoNames, len(m))
	for l, n := range m {
		out[l] = n
	}
	return out
}

// ListGeoDistricts — GET /v1/geo/cities/{cityId}/districts: районы с архивными; город модератора сверяет
// конвейер.
func (h *Handler) ListGeoDistricts(ctx context.Context, req oapi.ListGeoDistrictsRequestObject) (oapi.ListGeoDistrictsResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	ds, err := r.Districts(ctx, req.CityId)
	if err != nil {
		return nil, Problem(err)
	}
	body := oapi.GeoDistrictList{Items: make([]oapi.GeoDistrict, 0, len(ds))}
	for _, d := range ds {
		body.Items = append(body.Items, oapi.GeoDistrict{Id: d.ID, Name: d.Name, ArchivedAt: d.ArchivedAt, Version: d.Version})
	}
	return oapi.ListGeoDistricts200JSONResponse{Body: body,
		Headers: oapi.ListGeoDistricts200ResponseHeaders{CacheControl: cachePrivate}}, nil
}

// ListGeoCountries — GET /v1/geo/countries: все страны с правилами (geo.countries.read — всем ролям, R9).
func (h *Handler) ListGeoCountries(ctx context.Context, req oapi.ListGeoCountriesRequestObject) (oapi.ListGeoCountriesResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	cs, err := r.Countries(ctx, locale)
	if err != nil {
		return nil, Problem(err)
	}
	body := oapi.GeoCountryList{Items: make([]oapi.GeoCountry, 0, len(cs))}
	for _, c := range cs {
		body.Items = append(body.Items, countryOf(c))
	}
	return oapi.ListGeoCountries200JSONResponse{Body: body, Headers: oapi.ListGeoCountries200ResponseHeaders{
		CacheControl: cachePrivate, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

// countryOf — страна в схеме GeoCountry (список, правка страны).
func countryOf(c geo.AdminCountry) oapi.GeoCountry {
	return oapi.GeoCountry{
		Code: c.Code, Name: c.Name, Names: names(c.Names), Currency: c.Currency, DefaultLocale: c.DefaultLocale,
		PhonePrefix: c.PhonePrefix, WeekStartsOn: c.WeekStartsOn, MinSignupAge: c.MinSignupAge,
		AgeOfMajority: c.AgeOfMajority, IsEnabled: c.IsEnabled, OpenCities: c.OpenCities, Version: c.Version,
	}
}

// ListGeoRegions — GET /v1/geo/countries/{code}/regions: регионы страны (модератору — можно, R9).
func (h *Handler) ListGeoRegions(ctx context.Context, req oapi.ListGeoRegionsRequestObject) (oapi.ListGeoRegionsResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	rs, err := r.Regions(ctx, req.Code, locale)
	if err != nil {
		return nil, Problem(err)
	}
	body := oapi.GeoRegionList{Items: make([]oapi.GeoRegion, 0, len(rs))}
	for _, rg := range rs {
		body.Items = append(body.Items, oapi.GeoRegion{Id: rg.ID, Name: rg.Name, Names: names(rg.Names),
			GeonameAdmin1Code: rg.Admin1Code})
	}
	return oapi.ListGeoRegions200JSONResponse{Body: body, Headers: oapi.ListGeoRegions200ResponseHeaders{
		CacheControl: cachePrivate, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}
