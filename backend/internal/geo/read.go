package geo

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"

	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/geo/internal/store"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/page"
)

// citiesSet — имя набора полей курсора списка: (ранг статуса, population, id).
const citiesSet = "geo.cities(rank,population,id)"

var citiesKinds = []page.Kind{page.KindInt64, page.KindInt64, page.KindUUID}

// errBadPoint — координаты вне диапазона или не числа. HTTP-слой отвечает на это 400 раньше
// (спека §4.2); здесь — защита от ошибки PostGIS (500) при вызове не из HTTP.
var errBadPoint = errors.New("geo: координаты вне диапазона")

type reader struct{ st *store.Store }

func (r *reader) ListCities(ctx context.Context, p ListParams) (CityPage, error) {
	arg := geodb.ListCitiesParams{
		Locale:   nameLocale(p.Locale),
		Statuses: make([]string, 0, len(p.Status)),
		Country:  strings.ToUpper(p.Country),
	}
	for _, s := range p.Status {
		arg.Statuses = append(arg.Statuses, string(s))
	}
	if p.Q != "" {
		q, err := r.st.NormalizeQuery(ctx, p.Q)
		if err != nil {
			return CityPage{}, err
		}
		if q == "" {
			return CityPage{}, ErrBadQuery
		}
		arg.QPrefix = q
	}
	if p.Cursor != "" {
		k, err := page.DecodeKeyset(p.Cursor, citiesSet, citiesKinds...)
		if err != nil {
			return CityPage{}, err
		}
		if len(k.Values) != len(citiesKinds) {
			return CityPage{}, page.ErrBadCursor
		}
		rank, ok1 := k.Values[0].(int64)
		population, ok2 := k.Values[1].(int64)
		id, ok3 := k.Values[2].(uuid.UUID)
		if !ok1 || !ok2 || !ok3 {
			return CityPage{}, page.ErrBadCursor
		}
		arg.HasCursor, arg.AfterRank, arg.AfterPopulation, arg.AfterID = true, rank, population, id
	}
	limit := page.Limit(p.Limit)
	arg.RowLimit = int64(limit) + 1 // лишняя строка — признак следующей страницы
	rows, err := r.st.ListCities(ctx, arg)
	if err != nil {
		return CityPage{}, err
	}
	out := CityPage{Items: make([]City, 0, min(len(rows), limit))}
	for _, row := range rows[:min(len(rows), limit)] {
		out.Items = append(out.Items, newCity(row.ID, row.Slug, row.Name, row.Status, row.Timezone,
			row.Lat, row.Lon, row.RegionID, row.RegionName, row.CountryCode, row.CountryName))
	}
	if len(rows) > limit {
		last := rows[limit-1]
		out.Next = page.EncodeKeyset(page.Keyset{Set: citiesSet,
			Values: []any{last.StatusRank, last.Population, last.ID}}, citiesKinds...)
	}
	return out, nil
}

func (r *reader) City(ctx context.Context, id uuid.UUID, locale string) (CityDetails, error) {
	row, ok, err := r.st.VisibleCity(ctx, id, nameLocale(locale))
	if err != nil {
		return CityDetails{}, err
	}
	if !ok {
		return CityDetails{}, ErrCityNotFound
	}
	districts, err := r.st.ActiveDistricts(ctx, id)
	if err != nil {
		return CityDetails{}, err
	}
	d := CityDetails{
		City:          cityFromDetails(row),
		Currency:      row.Currency,
		DefaultLocale: row.DefaultLocale,
		PhonePrefix:   row.PhonePrefix,
		WeekStartsOn:  int(row.WeekStartsOn),
		MinSignupAge:  int(row.MinSignupAge),
		AgeOfMajority: int(row.AgeOfMajority),
		Districts:     make([]District, 0, len(districts)),
	}
	for _, x := range districts {
		d.Districts = append(d.Districts, District{ID: x.ID, Name: x.Name})
	}
	return d, nil
}

func (r *reader) CityBySlug(ctx context.Context, slug, locale string) (CityDetails, error) {
	id, ok, err := r.st.ResolveCitySlug(ctx, slug)
	if err != nil {
		return CityDetails{}, err
	}
	if !ok {
		return CityDetails{}, ErrCityNotFound
	}
	return r.City(ctx, id, locale)
}

func (r *reader) Nearest(ctx context.Context, p Point, locale string) (Nearest, error) {
	// ±Inf — вне диапазона; NaN проверяется явно: сравнения с ним всегда ложны. PostGIS сам не
	// откажет: geography молча сдвигает координаты в диапазон, а NaN пропускает.
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return Nearest{}, errBadPoint
	}
	loc := nameLocale(locale)
	var out Nearest
	open, ok, err := r.st.NearestOpenCity(ctx, p.Lat, p.Lon, loc)
	if err != nil {
		return Nearest{}, err
	}
	if ok {
		out.Open = &OpenCity{DistanceM: open.DistanceM, City: newCity(open.ID, open.Slug, open.Name, open.Status,
			open.Timezone, open.Lat, open.Lon, open.RegionID, open.RegionName, open.CountryCode, open.CountryName)}
	}
	if out.Here, err = r.here(ctx, p, loc); err != nil {
		return Nearest{}, err
	}
	return out, nil
}

// here — место, накрывающее точку (спека §4.3): кандидаты в HereSearchRadiusMeters отсортированы
// по расстоянию, первый с distance ≤ r(population) — ближайший из накрывающих. Его страна не
// видима (§4.1) — nil, следующий кандидат не берётся.
func (r *reader) here(ctx context.Context, p Point, loc string) (*Place, error) {
	cands, err := r.st.PlacesWithin(ctx, p.Lat, p.Lon, domain.HereSearchRadiusMeters)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(cands, func(c geodb.PlacesWithinRow) bool {
		return c.DistanceM <= domain.CoverRadiusMeters(c.Population)
	})
	if i < 0 {
		return nil, nil
	}
	row, ok, err := r.st.VisiblePlace(ctx, cands[i].GeonameID, loc)
	if err != nil || !ok {
		return nil, err
	}
	place := &Place{
		GeonameID: row.GeonameID,
		Name:      row.Name,
		Country:   Country{Code: row.CountryCode, Name: row.CountryName},
		DistanceM: cands[i].DistanceM,
	}
	if row.RegionName != "" {
		place.RegionName = &row.RegionName
	}
	if row.CityID != nil {
		city, found, err := r.st.VisibleCity(ctx, *row.CityID, loc)
		if err != nil {
			return nil, err
		}
		if found {
			c := cityFromDetails(city)
			place.City = &c
		}
	}
	return place, nil
}

func (r *reader) CitySettings(ctx context.Context, id uuid.UUID) (CitySettings, error) {
	row, ok, err := r.st.CitySettings(ctx, id)
	if err != nil {
		return CitySettings{}, err
	}
	if !ok {
		return CitySettings{}, ErrCityNotFound
	}
	return CitySettings{
		CityID:         row.ID,
		Timezone:       row.Timezone,
		Status:         Status(row.Status),
		CountryCode:    row.CountryCode,
		CountryEnabled: row.CountryEnabled,
		Currency:       row.Currency,
		DefaultLocale:  row.DefaultLocale,
		MinSignupAge:   int(row.MinSignupAge),
		AgeOfMajority:  int(row.AgeOfMajority),
	}, nil
}

// newCity — DTO из колонок, общих для запросов городов. regionID nil — региона нет (regionName
// тогда пуст).
func newCity(id uuid.UUID, slug, name, status, timezone string, lat, lon float64,
	regionID *uuid.UUID, regionName, countryCode, countryName string,
) City {
	c := City{
		ID: id, Slug: slug, Name: name, Status: Status(status), Timezone: timezone,
		Location: Point{Lat: lat, Lon: lon},
		Country:  Country{Code: countryCode, Name: countryName},
	}
	if regionID != nil {
		c.Region = &Ref{ID: *regionID, Name: regionName}
	}
	return c
}

func cityFromDetails(row geodb.GetVisibleCityRow) City {
	return newCity(row.ID, row.Slug, row.Name, row.Status, row.Timezone, row.Lat, row.Lon,
		row.RegionID, row.RegionName, row.CountryCode, row.CountryName)
}
