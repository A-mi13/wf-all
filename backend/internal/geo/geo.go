// Package geo — справочник стран, регионов, городов и районов (спека geo). Корневой пакет —
// API модуля для других модулей и HTTP-слоя (спека бэкенда §3): DTO, ошибки, интерфейс Service,
// конструктор. Чтения реализованы здесь же (read.go): internal/app импортирует корневой пакет
// (события, типы), поэтому корневой пакет не может импортировать app — был бы цикл.
package geo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo/internal/store"
)

// Status — статус города; открытый enum контракта (CityStatus).
type Status string

const (
	StatusWaitlist Status = "waitlist"
	StatusPilot    Status = "pilot"
	StatusLive     Status = "live"
)

// Point — точка WGS 84 в градусах.
type Point struct{ Lat, Lon float64 }

// Ref — ссылка на объект справочника с названием на языке ответа.
type Ref struct {
	ID   uuid.UUID
	Name string
}

// Country — страна в ответе: ISO 3166-1 alpha-2 и название на языке ответа.
type Country struct{ Code, Name string }

// City — город публичного справочника (схема City контракта).
type City struct {
	ID       uuid.UUID
	Slug     string
	Name     string
	Status   Status
	Timezone string
	Location Point
	Region   *Ref // nil — регион не задан
	Country  Country
}

// District — район города (без архивных).
type District struct {
	ID   uuid.UUID
	Name string
}

// CityDetails — город с правилами страны и районами (схема CityDetails).
type CityDetails struct {
	City
	Currency, DefaultLocale, PhonePrefix      string
	WeekStartsOn, MinSignupAge, AgeOfMajority int
	Districts                                 []District
}

// Place — место GeoNames, накрывающее точку (схема GeoPlace, спека §4.3).
type Place struct {
	GeonameID  int64
	Name       string
	RegionName *string
	Country    Country // страна города, если место заведено, иначе страна источника (§4.1)
	DistanceM  float64
	City       *City // место заведено городом и город видим
}

// OpenCity — ближайший открытый (pilot/live) город и расстояние до него.
type OpenCity struct {
	City      City
	DistanceM float64
}

// Nearest — ответ автоопределения: nil — не найдено.
type Nearest struct {
	Open *OpenCity
	Here *Place
}

// ListParams — параметры списка. Locale — уже согласованная локаль ("" — нет поддерживаемой:
// названия на языке страны каждой записи). Q — сырой ввод: нормализует сервис.
type ListParams struct {
	Status  []Status
	Country string
	Q       string
	Cursor  string
	Limit   *int
	Locale  string
}

// CityPage — страница списка; Next == "" — конец.
type CityPage struct {
	Items []City
	Next  string
}

// CitySettings — правила города для других модулей (спека §7): без фильтра видимости.
type CitySettings struct {
	CityID                      uuid.UUID
	Timezone                    string
	Status                      Status
	CountryCode                 string
	CountryEnabled              bool
	Currency, DefaultLocale     string
	MinSignupAge, AgeOfMajority int
}

var (
	// ErrCityNotFound — города нет или его страна выключена (404 geo.city_not_found).
	ErrCityNotFound = errors.New("geo: город не найден")
	// ErrBadQuery — q пуст после normalize_text (400 validation.failed).
	ErrBadQuery = errors.New("geo: пустой запрос после нормализации")
)

// Service — чтения справочника. locale — согласованная локаль ("" — языки стран записей).
type Service interface {
	ListCities(ctx context.Context, p ListParams) (CityPage, error)
	City(ctx context.Context, id uuid.UUID, locale string) (CityDetails, error)
	CityBySlug(ctx context.Context, slug, locale string) (CityDetails, error)
	Nearest(ctx context.Context, p Point, locale string) (Nearest, error)
	CitySettings(ctx context.Context, id uuid.UUID) (CitySettings, error)
}

// New — сервис чтения поверх пула (роль api или admin: нужен только SELECT).
func New(pool *pgxpool.Pool) Service { return &reader{st: store.New(pool)} }
