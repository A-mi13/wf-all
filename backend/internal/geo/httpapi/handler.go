// Package httpapi — публичные ручки модуля geo (тег geo контракта public, спека geo §4):
// справочник городов и автоопределение города по точке. Handler встраивается в
// internal/httpapi/public.Server: тег операции = модуль-реализатор (apitest.TagViolations).
package httpapi

import (
	"context"
	"errors"
	"math"
	"net/http"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/i18n"
)

// Заголовки ответов (спека geo §4.2).
const (
	cacheCatalog = "public, max-age=300" // список и город — справочник, общие кэши хранят до 5 минут
	cacheNearest = "private, no-store"   // ответ по координатам пользователя не кэшируется
	varyLanguage = "Accept-Language"     // названия зависят от языка запроса
)

// Handler — ручки geo поверх geo.Service.
type Handler struct{ svc geo.Service }

// New — хендлер для public.Server (cmd/api передаёт geo.New(pool) через public.Options.Geo).
func New(svc geo.Service) Handler { return Handler{svc: svc} }

// ListCities — GET /v1/cities: страница справочника по фильтрам.
func (h Handler) ListCities(ctx context.Context, req oapi.ListCitiesRequestObject) (oapi.ListCitiesResponseObject, error) {
	locale, lang := negotiate(req.Params.AcceptLanguage)
	p := geo.ListParams{Limit: req.Params.Limit, Locale: locale}
	if req.Params.Status != nil {
		for _, s := range *req.Params.Status {
			p.Status = append(p.Status, geo.Status(s))
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
	pg, err := h.svc.ListCities(ctx, p)
	if err != nil {
		return nil, problem(err)
	}
	body := oapi.CityPage{Items: make([]oapi.City, 0, len(pg.Items))}
	for _, c := range pg.Items {
		body.Items = append(body.Items, city(c))
	}
	if pg.Next != "" {
		next := pg.Next
		body.NextCursor = &next
	}
	return oapi.ListCities200JSONResponse{Body: body, Headers: oapi.ListCities200ResponseHeaders{
		CacheControl: cacheCatalog, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

// GetCity — GET /v1/cities/{cityId}.
func (h Handler) GetCity(ctx context.Context, req oapi.GetCityRequestObject) (oapi.GetCityResponseObject, error) {
	locale, lang := negotiate(req.Params.AcceptLanguage)
	d, err := h.svc.City(ctx, req.CityId, locale)
	if err != nil {
		return nil, problem(err)
	}
	return oapi.GetCity200JSONResponse{Body: details(d), Headers: oapi.GetCity200ResponseHeaders{
		CacheControl: cacheCatalog, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

// GetCityBySlug — GET /v1/cities/by-slug/{slug}: текущий или прошлый slug, в ответе — текущий.
func (h Handler) GetCityBySlug(ctx context.Context, req oapi.GetCityBySlugRequestObject) (oapi.GetCityBySlugResponseObject, error) {
	locale, lang := negotiate(req.Params.AcceptLanguage)
	d, err := h.svc.CityBySlug(ctx, req.Slug, locale)
	if err != nil {
		return nil, problem(err)
	}
	return oapi.GetCityBySlug200JSONResponse{Body: details(d), Headers: oapi.GetCityBySlug200ResponseHeaders{
		CacheControl: cacheCatalog, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

// FindNearestCity — GET /v1/cities/nearest. Координаты не логируются: лог доступа пишет путь без
// query, а сюда они приходят только для сценария.
func (h Handler) FindNearestCity(ctx context.Context, req oapi.FindNearestCityRequestObject) (oapi.FindNearestCityResponseObject, error) {
	if err := checkPoint(req.Params.Lat, req.Params.Lon); err != nil {
		return nil, err
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	n, err := h.svc.Nearest(ctx, geo.Point{Lat: req.Params.Lat, Lon: req.Params.Lon}, locale)
	if err != nil {
		return nil, problem(err)
	}
	var body oapi.NearestCity
	if n.Open != nil {
		body.NearestOpen = &oapi.OpenCity{City: city(n.Open.City), DistanceM: n.Open.DistanceM}
	}
	if n.Here != nil {
		here := place(*n.Here)
		body.Here = &here
	}
	return oapi.FindNearestCity200JSONResponse{Body: body, Headers: oapi.FindNearestCity200ResponseHeaders{
		CacheControl: cacheNearest, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

// checkPoint — явная проверка точки до сценария (спека geo §4.2): валидатор контракта NaN, ±Inf
// и выход за диапазон уже отвергает, но PostGIS принял бы их молча — хендлер не полагается на
// один слой.
func checkPoint(lat, lon float64) error {
	if err := checkCoord("query.lat", lat, 90); err != nil {
		return err
	}
	return checkCoord("query.lon", lon, 180)
}

func checkCoord(field string, v, limit float64) error {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return httpx.NewFieldError(field, "type")
	case v < -limit:
		return httpx.NewFieldError(field, "minimum")
	case v > limit:
		return httpx.NewFieldError(field, "maximum")
	}
	return nil
}

// negotiate — язык ответа по Accept-Language (спека geo §4.2; строки заголовка склеил
// httpx.CombineHeaders, через ", "). "" — поддерживаемого
// нет: названия на языке страны каждой записи, Content-Language не ставится.
func negotiate(header *string) (locale string, contentLanguage *string) {
	if header == nil {
		return "", nil
	}
	l, ok := i18n.Negotiate(*header, i18n.Supported)
	if !ok {
		return "", nil
	}
	return l, &l
}

// problem — ответ клиенту на ошибку сценария; прочие ошибки (в том числе ProblemError платформы —
// негодный курсор) уходят как есть: WriteError ответит их Problem или 500.
func problem(err error) error {
	switch {
	case errors.Is(err, geo.ErrCityNotFound):
		return httpx.NewError(http.StatusNotFound, geo.CodeCityNotFound)
	case errors.Is(err, geo.ErrBadQuery):
		// q прошёл схему (1–100 символов), но после normalize_text пуст: «!!!», одни пробелы
		return httpx.NewFieldError("query.q", "minLength")
	}
	return err
}

func city(c geo.City) oapi.City {
	out := oapi.City{
		Id: c.ID, Slug: c.Slug, Name: c.Name, Status: oapi.CityStatus(c.Status), Timezone: c.Timezone,
		Location: oapi.GeoPoint{Lat: c.Location.Lat, Lon: c.Location.Lon},
		Country:  oapi.CountryRef{Code: c.Country.Code, Name: c.Country.Name},
	}
	if c.Region != nil {
		out.Region = &oapi.RegionRef{Id: c.Region.ID, Name: c.Region.Name}
	}
	return out
}

func details(d geo.CityDetails) oapi.CityDetails {
	c := city(d.City)
	out := oapi.CityDetails{
		Id: c.Id, Slug: c.Slug, Name: c.Name, Status: c.Status, Timezone: c.Timezone,
		Location: c.Location, Region: c.Region, Country: c.Country,
		Currency: d.Currency, DefaultLocale: d.DefaultLocale, PhonePrefix: d.PhonePrefix,
		WeekStartsOn: d.WeekStartsOn, MinSignupAge: d.MinSignupAge, AgeOfMajority: d.AgeOfMajority,
		// массив обязателен и не null: город без районов — []
		Districts: make([]oapi.District, 0, len(d.Districts)),
	}
	for _, x := range d.Districts {
		out.Districts = append(out.Districts, oapi.District{Id: x.ID, Name: x.Name})
	}
	return out
}

func place(p geo.Place) oapi.GeoPlace {
	out := oapi.GeoPlace{
		GeonameId: p.GeonameID, Name: p.Name, RegionName: p.RegionName, DistanceM: p.DistanceM,
		Country: oapi.CountryRef{Code: p.Country.Code, Name: p.Country.Name},
	}
	if p.City != nil {
		c := city(*p.City)
		out.City = &c
	}
	return out
}
