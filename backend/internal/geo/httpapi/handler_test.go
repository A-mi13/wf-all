package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	geohttp "wf/backend/internal/geo/httpapi"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// stub — geo.Service: заданная ошибка или заданная карточка; считает вызовы, помнит точку.
type stub struct {
	err     error
	details geo.CityDetails
	calls   int
	point   geo.Point
}

func (s *stub) ListCities(context.Context, geo.ListParams) (geo.CityPage, error) {
	s.calls++
	return geo.CityPage{}, s.err
}

func (s *stub) City(context.Context, uuid.UUID, string) (geo.CityDetails, error) {
	s.calls++
	return s.details, s.err
}

func (s *stub) CityBySlug(context.Context, string, string) (geo.CityDetails, error) {
	s.calls++
	return s.details, s.err
}

func (s *stub) Nearest(_ context.Context, p geo.Point, _ string) (geo.Nearest, error) {
	s.calls++
	s.point = p
	return geo.Nearest{}, s.err
}

func (s *stub) CitySettings(context.Context, uuid.UUID) (geo.CitySettings, error) {
	s.calls++
	return geo.CitySettings{}, s.err
}

func problemOf(t *testing.T, err error) httpx.Problem {
	t.Helper()
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !ok {
		t.Fatalf("ошибка %v — не ProblemError", err)
	}
	return pe.Problem()
}

func nearest(lat, lon float64) oapi.FindNearestCityRequestObject {
	return oapi.FindNearestCityRequestObject{Params: oapi.FindNearestCityParams{Lat: lat, Lon: lon}}
}

// Явная проверка точки до сценария (спека geo §4.2, Review Focus 2): PostGIS молча принимает NaN и
// координаты вне диапазона, а валидатор контракта — один слой; хендлер на него не полагается.
func TestFindNearestCityRejectsBadPoint(t *testing.T) {
	for _, c := range []struct {
		name     string
		lat, lon float64
		want     httpx.FieldError
	}{
		{"NaN в широте", math.NaN(), 0, httpx.FieldError{Field: "query.lat", Code: "type"}},
		{"NaN в долготе", 0, math.NaN(), httpx.FieldError{Field: "query.lon", Code: "type"}},
		{"+Inf", math.Inf(1), 0, httpx.FieldError{Field: "query.lat", Code: "type"}},
		{"-Inf", 0, math.Inf(-1), httpx.FieldError{Field: "query.lon", Code: "type"}},
		{"широта за 90", 90.0001, 0, httpx.FieldError{Field: "query.lat", Code: "maximum"}},
		{"широта ниже -90", -90.0001, 0, httpx.FieldError{Field: "query.lat", Code: "minimum"}},
		{"долгота за 180", 0, 180.0001, httpx.FieldError{Field: "query.lon", Code: "maximum"}},
		{"долгота ниже -180", 0, -180.0001, httpx.FieldError{Field: "query.lon", Code: "minimum"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &stub{}
			_, err := geohttp.New(s).FindNearestCity(context.Background(), nearest(c.lat, c.lon))
			p := problemOf(t, err)
			if p.Status != http.StatusBadRequest || p.Code != httpx.CodeValidationFailed ||
				!reflect.DeepEqual(p.Errors, []httpx.FieldError{c.want}) {
				t.Fatalf("problem = %+v, want 400 validation.failed %+v", p, c.want)
			}
			if s.calls != 0 {
				t.Fatal("сценарий вызван с негодной точкой")
			}
		})
	}
}

// Края диапазона — точка доходит до сценария как есть: полюса, антимеридиан, Чукотка (lon < 0).
func TestFindNearestCityAcceptsEdges(t *testing.T) {
	for _, p := range []geo.Point{{Lat: 90, Lon: 180}, {Lat: -90, Lon: -180}, {Lat: 64.73, Lon: -179.9}} {
		s := &stub{}
		if _, err := geohttp.New(s).FindNearestCity(context.Background(), nearest(p.Lat, p.Lon)); err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		if s.point != p {
			t.Fatalf("сценарий получил %+v, want %+v", s.point, p)
		}
	}
}

// Ошибки сценария → ответы контракта; прочие уходят как есть (WriteError: ProblemError платформы —
// её ответ, иначе 500).
func TestErrorsMapToContractCodes(t *testing.T) {
	ctx := context.Background()
	_, err := geohttp.New(&stub{err: fmt.Errorf("geo: город: %w", geo.ErrCityNotFound)}).
		GetCity(ctx, oapi.GetCityRequestObject{CityId: uuid.New()})
	if p := problemOf(t, err); p.Status != http.StatusNotFound || p.Code != geo.CodeCityNotFound {
		t.Fatalf("getCity: %+v", p)
	}
	_, err = geohttp.New(&stub{err: geo.ErrCityNotFound}).
		GetCityBySlug(ctx, oapi.GetCityBySlugRequestObject{Slug: "nowhere"})
	if p := problemOf(t, err); p.Status != http.StatusNotFound || p.Code != geo.CodeCityNotFound {
		t.Fatalf("getCityBySlug: %+v", p)
	}
	_, err = geohttp.New(&stub{err: geo.ErrBadQuery}).ListCities(ctx, oapi.ListCitiesRequestObject{})
	if p := problemOf(t, err); p.Status != http.StatusBadRequest || p.Code != httpx.CodeValidationFailed ||
		!reflect.DeepEqual(p.Errors, []httpx.FieldError{{Field: "query.q", Code: "minLength"}}) {
		t.Fatalf("listCities, пустой q: %+v", p)
	}
	boom := errors.New("pgx: сломалось")
	_, err = geohttp.New(&stub{err: boom}).FindNearestCity(ctx, nearest(45.04, 41.97))
	if !errors.Is(err, boom) {
		t.Fatalf("сбой подменён: %v", err)
	}
	if _, ok := errors.AsType[httpx.ProblemError](err); ok {
		t.Fatal("сбой хранилища стал ответом клиенту, а не 500")
	}
}

// Город без региона и районов: region — null, districts — [] (поле обязательное, массив не null).
func TestCityWithoutRegionAndDistricts(t *testing.T) {
	s := &stub{details: geo.CityDetails{City: geo.City{ID: uuid.New(), Slug: "zarechnyy", Name: "Заречный",
		Status: geo.Status("waitlist"), Timezone: "Europe/Moscow", Country: geo.Country{Code: "RU", Name: "Россия"}}}}
	resp, err := geohttp.New(s).GetCity(context.Background(), oapi.GetCityRequestObject{CityId: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	ok, isOK := resp.(oapi.GetCity200JSONResponse)
	if !isOK {
		t.Fatalf("ответ %T", resp)
	}
	b, err := json.Marshal(ok.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"region":null`) || !strings.Contains(string(b), `"districts":[]`) {
		t.Fatalf("тело: %s", b)
	}
}
