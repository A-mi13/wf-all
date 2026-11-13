package public_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/token"
)

var (
	stavropolID = uuid.MustParse("3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47")
	stavropol   = geo.City{
		ID: stavropolID, Slug: "stavropol", Name: "Ставрополь", Status: geo.Status("pilot"),
		Timezone: "Europe/Moscow", Location: geo.Point{Lat: 45.03442, Lon: 41.9642},
		Region:  &geo.Ref{ID: uuid.MustParse("b7e4d2c9-1a3f-4e8b-9c6d-5f0a2e7b1c38"), Name: "Ставропольский край"},
		Country: geo.Country{Code: "RU", Name: "Россия"},
	}
)

// fakeGeo — geo.Service конвейерных тестов: отдаёт Ставрополь и запоминает вызовы. err задан —
// его возвращает любой метод.
type fakeGeo struct {
	err     error
	next    string       // курсор следующей страницы списка
	nearest *geo.Nearest // ответ Nearest; nil — Ставрополь в 1,8 км и место «Ставрополь»
	calls   []string
	locale  string
	list    geo.ListParams
	id      uuid.UUID
	slug    string
	point   geo.Point
}

func (f *fakeGeo) ListCities(_ context.Context, p geo.ListParams) (geo.CityPage, error) {
	f.calls, f.list, f.locale = append(f.calls, "ListCities"), p, p.Locale
	if f.err != nil {
		return geo.CityPage{}, f.err
	}
	return geo.CityPage{Items: []geo.City{stavropol}, Next: f.next}, nil
}

func (f *fakeGeo) City(_ context.Context, id uuid.UUID, locale string) (geo.CityDetails, error) {
	f.calls, f.id, f.locale = append(f.calls, "City"), id, locale
	return f.details()
}

func (f *fakeGeo) CityBySlug(_ context.Context, slug, locale string) (geo.CityDetails, error) {
	f.calls, f.slug, f.locale = append(f.calls, "CityBySlug"), slug, locale
	return f.details()
}

func (f *fakeGeo) details() (geo.CityDetails, error) {
	if f.err != nil {
		return geo.CityDetails{}, f.err
	}
	return geo.CityDetails{
		City: stavropol, Currency: "RUB", DefaultLocale: "ru", PhonePrefix: "+7",
		WeekStartsOn: 1, MinSignupAge: 14, AgeOfMajority: 18,
		Districts: []geo.District{{ID: uuid.MustParse("c1d9e3a7-5b2f-4a8c-8e6d-0f4b7a2c9e15"), Name: "Промышленный"}},
	}, nil
}

func (f *fakeGeo) Nearest(_ context.Context, p geo.Point, locale string) (geo.Nearest, error) {
	f.calls, f.point, f.locale = append(f.calls, "Nearest"), p, locale
	if f.err != nil {
		return geo.Nearest{}, f.err
	}
	if f.nearest != nil {
		return *f.nearest, nil
	}
	region, c := "Ставропольский край", stavropol
	return geo.Nearest{
		Open: &geo.OpenCity{City: stavropol, DistanceM: 1840.5},
		Here: &geo.Place{GeonameID: 487846, Name: "Ставрополь", RegionName: &region,
			Country: geo.Country{Code: "RU", Name: "Россия"}, DistanceM: 1840.5, City: &c},
	}, nil
}

// CitySettings публичный API не вызывает.
func (f *fakeGeo) CitySettings(context.Context, uuid.UUID) (geo.CitySettings, error) {
	f.calls = append(f.calls, "CitySettings")
	return geo.CitySettings{}, errors.New("CitySettings не вызывается публичным API")
}

// ExistingCityIDs публичный API не вызывает (проверка городов флагов — админка, R19).
func (f *fakeGeo) ExistingCityIDs(context.Context, []uuid.UUID) ([]uuid.UUID, error) {
	f.calls = append(f.calls, "ExistingCityIDs")
	return nil, errors.New("ExistingCityIDs не вызывается публичным API")
}

// fakeVersions — appversion.Reader: заданный ответ и запомненная платформа.
type fakeVersions struct {
	v     appversion.Versions
	ok    bool
	err   error
	asked appversion.Platform
}

func (f *fakeVersions) Get(_ context.Context, p appversion.Platform) (appversion.Versions, bool, error) {
	f.asked = p
	return f.v, f.ok, f.err
}

// fakeSession — подделка загрузчика сессий для операций с замком (до этапа сессий identity в public собран
// auth.NoSessions — любой токен 401): одна живая сессия, её access-токен выпущен своим token.Issuer.
type fakeSession struct {
	userID, sessionID uuid.UUID
	token             string
}

func (f *fakeSession) LoadSession(_ context.Context, id uuid.UUID) (*auth.Principal, error) {
	if id != f.sessionID {
		return nil, auth.ErrSessionInvalid
	}
	return &auth.Principal{UserID: f.userID, SessionID: f.sessionID, Status: "active"}, nil
}

// withFakeSession — o получает выпускающего токены и загрузчик с одной живой сессией; токен — в результате.
func withFakeSession(t *testing.T, o *public.Options) *fakeSession {
	t.Helper()
	seed, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	k, err := token.ParseKeys([]string{seed})
	if err != nil {
		t.Fatal(err)
	}
	issuer := token.NewIssuer(k, clock.System)
	f := &fakeSession{userID: uuid.Must(uuid.NewV7()), sessionID: uuid.Must(uuid.NewV7())}
	if f.token, _, err = issuer.Access(f.userID, f.sessionID); err != nil {
		t.Fatal(err)
	}
	o.Tokens, o.Sessions = issuer, f
	return f
}
