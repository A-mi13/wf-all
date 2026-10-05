// Package store — доступ модуля geo к своей схеме: sqlc-пакет geodb и тонкий репозиторий поверх
// него. Строки отдаются как есть (типы geodb): в DTO их переводит корневой пакет geo, потому что
// store не может импортировать geo (цикл geo → store → geo).
package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/geo/internal/store/geodb"
)

// Store — запросы geo поверх пула или транзакции.
type Store struct{ q *geodb.Queries }

func New(db geodb.DBTX) *Store { return &Store{q: geodb.New(db)} }

// one — результат :one-запроса: нет строки — found=false без ошибки.
func one[T any](row T, err error) (T, bool, error) {
	var zero T
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return zero, false, nil
	case err != nil:
		return zero, false, err
	}
	return row, true, nil
}

// NormalizeQuery — строка поиска через normalize_text (как name_normalized в схеме).
func (s *Store) NormalizeQuery(ctx context.Context, input string) (string, error) {
	return s.q.NormalizeQuery(ctx, input)
}

func (s *Store) ListCities(ctx context.Context, p geodb.ListCitiesParams) ([]geodb.ListCitiesRow, error) {
	return s.q.ListCities(ctx, p)
}

// VisibleCity — город видимой страны с названиями на locale.
func (s *Store) VisibleCity(ctx context.Context, id uuid.UUID, locale string) (geodb.GetVisibleCityRow, bool, error) {
	return one(s.q.GetVisibleCity(ctx, geodb.GetVisibleCityParams{ID: id, Locale: locale}))
}

// ResolveCitySlug — id города по текущему slug или по истории.
func (s *Store) ResolveCitySlug(ctx context.Context, slug string) (uuid.UUID, bool, error) {
	return one(s.q.ResolveCitySlug(ctx, slug))
}

func (s *Store) ActiveDistricts(ctx context.Context, cityID uuid.UUID) ([]geodb.ListActiveDistrictsRow, error) {
	return s.q.ListActiveDistricts(ctx, cityID)
}

func (s *Store) NearestOpenCity(ctx context.Context, lat, lon float64, locale string) (geodb.NearestOpenCityRow, bool, error) {
	return one(s.q.NearestOpenCity(ctx, geodb.NearestOpenCityParams{Lat: lat, Lon: lon, Locale: locale}))
}

func (s *Store) PlacesWithin(ctx context.Context, lat, lon, radiusM float64) ([]geodb.PlacesWithinRow, error) {
	return s.q.PlacesWithin(ctx, geodb.PlacesWithinParams{Lat: lat, Lon: lon, RadiusM: radiusM})
}

// VisiblePlace — место источника, если его страна (§4.1) есть и включена.
func (s *Store) VisiblePlace(ctx context.Context, geonameID int64, locale string) (geodb.GetVisiblePlaceRow, bool, error) {
	return one(s.q.GetVisiblePlace(ctx, geodb.GetVisiblePlaceParams{GeonameID: geonameID, Locale: locale}))
}

func (s *Store) CitySettings(ctx context.Context, id uuid.UUID) (geodb.GetCitySettingsRow, bool, error) {
	return one(s.q.GetCitySettings(ctx, id))
}
