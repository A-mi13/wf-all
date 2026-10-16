// Package admin — операции модуля geo в админке (тег geo контракта admin). План A — список городов
// для выбора городов модератора (D24, P28, M10); план B расширяет.
package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/i18n"
)

// permCitiesRead — право чтения городов (perm, §2.5): модератору — scoped, только его города.
const permCitiesRead = "geo.cities.read"

// Заголовки ответа списка (контракт listGeoCities).
const (
	cachePrivate = "private, no-store" // ответ зависит от сотрудника и его городов
	varyLanguage = "Accept-Language"   // названия зависят от языка запроса
)

// Handler — операции тега geo; встраивается в internal/httpapi/admin.Server указателем. Собирается New;
// нулевой Handler (cmd/admin-api до сборки 10c) отвечает 501 http.not_implemented.
type Handler struct{ svc geo.Service }

// New — хендлер поверх чтения справочника (geo.New на пуле admin-api).
func New(svc geo.Service) *Handler { return &Handler{svc: svc} }

var (
	errNotImplemented  = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)
	errUnauthenticated = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)
	errForbidden       = httpx.NewError(http.StatusForbidden, httpx.CodeForbidden)
)

// ListGeoCities — GET /v1/geo/cities: города всех статусов; q — начало названия, ids — только эти (P28).
// Модератору (право scoped) — только его города: ids пересекаются с ними, без ids — они сами; скоуп — по
// GrantScoped, а не по len(CityIDs): модератор без городов получает пустой список, не весь справочник (M10).
func (h *Handler) ListGeoCities(ctx context.Context, req oapi.ListGeoCitiesRequestObject) (oapi.ListGeoCitiesResponseObject, error) {
	if h == nil || h.svc == nil { // нулевой хендлер или сервер без geo (Server{})
		return nil, errNotImplemented
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		// без сессии до хендлера не доходят (валидатор), это защита в глубину
		return nil, errUnauthenticated
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	p := geo.ListParams{Limit: req.Params.Limit, Locale: locale}
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
	pg, err := h.svc.ListCities(ctx, p)
	if errors.Is(err, geo.ErrBadQuery) {
		// q прошёл схему (1–100 символов), но после normalize_text пуст: «!!!», одни пробелы
		return nil, httpx.NewFieldError("query.q", "minLength")
	}
	if err != nil {
		return nil, err // в том числе page.ErrBadCursor — 400 по query.cursor
	}
	body := oapi.GeoCityPage{Items: make([]oapi.GeoCity, 0, len(pg.Items))}
	for _, c := range pg.Items {
		out := oapi.GeoCity{Id: c.ID, Slug: c.Slug, Name: c.Name, Status: oapi.CityStatus(c.Status), CountryCode: c.Country.Code}
		if c.Region != nil {
			name := c.Region.Name
			out.RegionName = &name
		}
		body.Items = append(body.Items, out)
	}
	if pg.Next != "" {
		next := pg.Next
		body.NextCursor = &next
	}
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

// negotiate — язык ответа по Accept-Language (строки заголовка склеены конвейером, httpx.CombineHeaders).
// "" — поддерживаемого нет: названия на языке страны каждой записи, Content-Language не ставится.
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
