// Package admin — операции модуля geo в админке (тег geo контракта admin). План A — список городов
// для выбора городов модератора (D24, P28, M10); план B — чтения (read.go, places.go, imports_read.go) и
// сценарии записи (заглушки — до задач 10–14).
package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/i18n"
)

// Заголовки ответов чтения (контракт: CachePrivate, Vary, ContentLanguage).
const (
	cachePrivate = "private, no-store" // ответ зависит от сотрудника и его городов
	varyLanguage = "Accept-Language"   // названия зависят от языка запроса
)

// Handler — операции тега geo; встраивается в internal/httpapi/admin.Server указателем. Собирается New;
// нулевой Handler (тесты конвейера; cmd/admin-api собирает настоящий) отвечает 501 http.not_implemented.
type Handler struct {
	svc  geo.Service
	read *app.AdminReader // чтения админки (план B, Task 9); nil — 501
}

// Deps — зависимости сценариев админки geo на пуле admin-api (план B; Task 16 дополняет часами, очередью и
// журналом для записи).
type Deps struct {
	Pool *pgxpool.Pool
}

// New — хендлер поверх чтения справочника (geo.New на пуле admin-api) и сценариев админки (d.Pool — тот же
// пул; nil — операции плана B отвечают 501, как нулевой Handler).
func New(svc geo.Service, d Deps) *Handler {
	h := &Handler{svc: svc}
	if d.Pool != nil {
		h.read = app.NewAdminReader(d.Pool)
	}
	return h
}

var (
	errNotImplemented  = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)
	errUnauthenticated = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)
	errForbidden       = httpx.NewError(http.StatusForbidden, httpx.CodeForbidden)
	errBadQuery        = httpx.NewFieldError("query.q", "minLength")
)

// reader — чтения админки; nil — хендлер не собран (нулевой или без Deps.Pool): 501.
func (h *Handler) reader() (*app.AdminReader, error) {
	if h == nil || h.read == nil {
		return nil, errNotImplemented
	}
	return h.read, nil
}

// problems — ошибки модуля и их ответы (план B, Приложение 3; коды — geo/codes.go).
var problems = []struct {
	err    error
	status int
	code   string
}{
	{geo.ErrCityNotFound, http.StatusNotFound, geo.CodeCityNotFound},
	{geo.ErrDistrictNotFound, http.StatusNotFound, geo.CodeDistrictNotFound},
	{geo.ErrPlaceNotFound, http.StatusNotFound, geo.CodePlaceNotFound},
	{geo.ErrCountryNotFound, http.StatusNotFound, geo.CodeCountryNotFound},
	{geo.ErrImportNotFound, http.StatusNotFound, geo.CodeImportNotFound},
	{geo.ErrImportJobNotFound, http.StatusNotFound, geo.CodeImportJobNotFound},
	{geo.ErrCityAlreadyActive, http.StatusConflict, geo.CodeCityAlreadyActive},
	{geo.ErrCityAlreadyLinked, http.StatusConflict, geo.CodeCityAlreadyLinked},
	{geo.ErrSlugTaken, http.StatusConflict, geo.CodeSlugTaken},
	{geo.ErrCountryExists, http.StatusConflict, geo.CodeCountryExists},
	{geo.ErrImportInProgress, http.StatusConflict, geo.CodeImportInProgress},
	{geo.ErrImportJobNotCancellable, http.StatusConflict, geo.CodeImportJobNotCancellable},
	{geo.ErrDistrictNameTaken, http.StatusConflict, geo.CodeDistrictNameTaken},
	{geo.ErrCountryNotEnabled, http.StatusUnprocessableEntity, geo.CodeCountryNotEnabled},
	{geo.ErrRegionNotFound, http.StatusUnprocessableEntity, geo.CodeRegionNotFound},
}

// Problem — ответ на ошибку сценария geo: ошибка модуля — её код (Приложение 3), q пуст после нормализации —
// 400 query.q minLength; прочее (page.ErrBadCursor и другие ProblemError, сбои) — как есть: WriteError ответит
// их Problem или 500.
func Problem(err error) error {
	if errors.Is(err, geo.ErrBadQuery) {
		return errBadQuery
	}
	for _, p := range problems {
		if errors.Is(err, p.err) {
			return httpx.NewError(p.status, p.code)
		}
	}
	return err
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

// Операции плана B, объявленные контрактом раньше хендлеров (Task 7, R17): отвечают 501 http.not_implemented
// и перечислены в pendingOps (internal/httpapi/admin/nostub_test.go). Задача из комментария заменяет
// заглушку хендлером и убирает операцию из pendingOps.

// ActivateGeoCity — заглушка до Task 10.
func (h *Handler) ActivateGeoCity(context.Context, oapi.ActivateGeoCityRequestObject) (oapi.ActivateGeoCityResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateGeoCity — заглушка до Task 11.
func (h *Handler) UpdateGeoCity(context.Context, oapi.UpdateGeoCityRequestObject) (oapi.UpdateGeoCityResponseObject, error) {
	return nil, errNotImplemented
}

// LinkGeoCity — заглушка до Task 11.
func (h *Handler) LinkGeoCity(context.Context, oapi.LinkGeoCityRequestObject) (oapi.LinkGeoCityResponseObject, error) {
	return nil, errNotImplemented
}

// CreateGeoDistrict — заглушка до Task 12.
func (h *Handler) CreateGeoDistrict(context.Context, oapi.CreateGeoDistrictRequestObject) (oapi.CreateGeoDistrictResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateGeoDistrict — заглушка до Task 12.
func (h *Handler) UpdateGeoDistrict(context.Context, oapi.UpdateGeoDistrictRequestObject) (oapi.UpdateGeoDistrictResponseObject, error) {
	return nil, errNotImplemented
}

// CreateGeoCountry — заглушка до Task 13.
func (h *Handler) CreateGeoCountry(context.Context, oapi.CreateGeoCountryRequestObject) (oapi.CreateGeoCountryResponseObject, error) {
	return nil, errNotImplemented
}

// UpdateGeoCountry — заглушка до Task 13.
func (h *Handler) UpdateGeoCountry(context.Context, oapi.UpdateGeoCountryRequestObject) (oapi.UpdateGeoCountryResponseObject, error) {
	return nil, errNotImplemented
}

// StartGeoImport — заглушка до Task 14.
func (h *Handler) StartGeoImport(context.Context, oapi.StartGeoImportRequestObject) (oapi.StartGeoImportResponseObject, error) {
	return nil, errNotImplemented
}

// GetGeoImportJob — заглушка до Task 14.
func (h *Handler) GetGeoImportJob(context.Context, oapi.GetGeoImportJobRequestObject) (oapi.GetGeoImportJobResponseObject, error) {
	return nil, errNotImplemented
}

// CancelGeoImportJob — заглушка до Task 14.
func (h *Handler) CancelGeoImportJob(context.Context, oapi.CancelGeoImportJobRequestObject) (oapi.CancelGeoImportJobResponseObject, error) {
	return nil, errNotImplemented
}
