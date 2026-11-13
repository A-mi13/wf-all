// Package geo — справочник стран, регионов, городов и районов (спека geo). Корневой пакет —
// API модуля для других модулей и HTTP-слоя (спека бэкенда §3): DTO, ошибки, интерфейс Service,
// конструктор. Публичные чтения реализованы здесь же (read.go): internal/app импортирует корневой пакет
// (события, типы), поэтому корневой пакет не может импортировать app — был бы цикл. Чтения и сценарии
// админки — internal/app (AdminReader, Admin) с DTO отсюда; их вызывает geo/admin.
package geo

import (
	"context"
	"errors"
	"fmt"
	"time"

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
// названия на языке страны каждой записи). Q — сырой ввод: нормализует сервис. IDs (P28, M10): nil — без
// фильтра; не nil — только эти города (несуществующие пропускаются), пустой не nil — ни одного: модератору
// вызывающий передаёт его города (или их пересечение с запрошенными) всегда не nil.
type ListParams struct {
	Status  []Status
	IDs     []uuid.UUID
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
	// ErrBadPoint — координаты вне диапазона или не числа. HTTP-слой отвечает 400 раньше (спека §4.2); здесь —
	// защита от ошибки PostGIS при вызове не из HTTP, а дошедшая до HTTP — тоже 400 validation.failed.
	ErrBadPoint = errors.New("geo: координаты вне диапазона")
)

// Ошибки админки geo (план B, Приложение 3): каждой — код в codes.go, ответ — geo/admin (ProblemOf).
// Объявлены все сразу (Task 9): их ждут сценарии Task 10–14.
var (
	ErrDistrictNotFound        = errors.New("geo: района нет или он не из города")       // 404 geo.district_not_found
	ErrPlaceNotFound           = errors.New("geo: места GeoNames нет или оно пропало")   // 404 geo.place_not_found
	ErrCountryNotFound         = errors.New("geo: страны нет в справочнике")             // 404 geo.country_not_found
	ErrImportNotFound          = errors.New("geo: строки журнала импорта нет")           // 404 geo.import_not_found
	ErrImportJobNotFound       = errors.New("geo: задачи импорта нет")                   // 404 geo.import_job_not_found
	ErrCityAlreadyActive       = errors.New("geo: на месте GeoNames уже есть город")     // 409 geo.city_already_active
	ErrCityAlreadyLinked       = errors.New("geo: город уже привязан к месту GeoNames")  // 409 geo.city_already_linked
	ErrSlugTaken               = errors.New("geo: адрес занят другим городом")           // 409 geo.slug_taken
	ErrCountryExists           = errors.New("geo: страна с этим кодом уже есть")         // 409 geo.country_exists
	ErrImportInProgress        = errors.New("geo: импорт страны уже в очереди или идёт") // 409 geo.import_in_progress
	ErrImportJobNotCancellable = errors.New("geo: задачу импорта уже не отменить")       // 409 geo.import_job_not_cancellable
	ErrDistrictNameTaken       = errors.New("geo: название занято действующим районом")  // 409 geo.district_name_taken
	ErrCountryNotEnabled       = errors.New("geo: страна выключена или её нет")          // 422 geo.country_not_enabled
	ErrRegionNotFound          = errors.New("geo: регион не из страны города")           // 422 geo.region_not_found
)

// Service — чтения справочника. locale — согласованная локаль ("" — языки стран записей).
type Service interface {
	ListCities(ctx context.Context, p ListParams) (CityPage, error)
	City(ctx context.Context, id uuid.UUID, locale string) (CityDetails, error)
	CityBySlug(ctx context.Context, slug, locale string) (CityDetails, error)
	Nearest(ctx context.Context, p Point, locale string) (Nearest, error)
	CitySettings(ctx context.Context, id uuid.UUID) (CitySettings, error)
	// ExistingCityIDs — какие из ids есть в cities: все статусы и страны, без фильтра видимости (города флага,
	// R19). Порядок — по id, повторы схлопнуты; пустой ввод — пустой результат.
	ExistingCityIDs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error)
}

// --- Админка (план B, Task 9; схемы Приложения 2) ---

// PlaceQuery — параметры listGeoPlaces: Q — сырой ввод (нормализует сервис), Country — код страны места (§4.1).
type PlaceQuery struct {
	Q, Country, Cursor, Locale string
	Limit                      *int
}

// AdminPlace — место GeoNames для активации (GeoPlace). CityID — город на месте (тогда страна — его).
type AdminPlace struct {
	GeonameID                    int64
	Name, ASCIIName, FeatureCode string
	Population                   int64
	Timezone                     string
	RegionName                   *string
	Country                      Country
	CityID                       *uuid.UUID
}

// PlacePage — страница мест; Next == "" — конец.
type PlacePage struct {
	Items []AdminPlace
	Next  string
}

// AdminCityQuery — параметры listGeoCities админки: как ListParams плюс RegionID (nil — без фильтра).
type AdminCityQuery struct {
	Status   []Status
	IDs      []uuid.UUID // nil — без фильтра; пустой не nil — ни одного (модератор, M10)
	Country  string
	RegionID *uuid.UUID
	Q        string
	Cursor   string
	Limit    *int
	Locale   string
}

// AdminCityItem — город списка админки (GeoCity): все страны, CountryEnabled в ответе.
type AdminCityItem struct {
	ID             uuid.UUID
	Slug, Name     string
	Status         Status
	CountryCode    string
	CountryEnabled bool
	Region         *Ref
	GeonameID      *int64
	Population     int64
	DistrictsCount int
}

// AdminCityPage — страница городов админки; Next == "" — конец.
type AdminCityPage struct {
	Items []AdminCityItem
	Next  string
}

// SlugEntry — прежний адрес города.
type SlugEntry struct {
	Slug       string
	ReplacedAt time.Time
}

// CityCountry — страна карточки города: код, название на языке ответа, включена ли.
type CityCountry struct {
	Code, Name string
	IsEnabled  bool
}

// AdminCity — карточка города админки (GeoCityDetails).
type AdminCity struct {
	ID              uuid.UUID
	Slug            string
	SlugHistory     []SlugEntry // новые сверху
	Name            string
	Names           map[string]string // все языки
	CanonicalLocale string
	Status          Status
	GeonameID       *int64
	Population      int64
	Timezone        string
	Location        Point
	Country         CityCountry
	Region          *Ref
	DistrictsCount  int
	Version         int64
}

// AdminDistrict — район с архивом и версией (GeoDistrict).
type AdminDistrict struct {
	ID         uuid.UUID
	Name       string
	ArchivedAt *time.Time
	Version    int64
}

// AdminCountry — страна с правилами (GeoCountry).
type AdminCountry struct {
	Code, Name                                string
	Names                                     map[string]string
	Currency, DefaultLocale, PhonePrefix      string
	WeekStartsOn, MinSignupAge, AgeOfMajority int
	IsEnabled                                 bool
	OpenCities                                int
	Version                                   int64
}

// AdminRegion — регион страны (GeoRegion).
type AdminRegion struct {
	ID         uuid.UUID
	Name       string
	Names      map[string]string
	Admin1Code *string
}

// ImportQuery — параметры listGeoImports.
type ImportQuery struct {
	Country string
	JobID   *int64
	Cursor  string
	Limit   *int
}

// Import — строка журнала импорта (GeoImport). StartedByLabel — подпись сотрудника из аудита постановки (R3).
type Import struct {
	ID                                                          uuid.UUID
	CountryCode, Status                                         string
	StartedAt                                                   time.Time
	FinishedAt                                                  *time.Time
	StartedBy                                                   *uuid.UUID
	StartedByLabel                                              *string
	RiverJobID                                                  *int64
	PlacesUpserted, PlacesRemoved, PlacesMissing, NamesUpserted *int
	Error                                                       *string
}

// ImportPage — страница журнала; Next == "" — конец.
type ImportPage struct {
	Items []Import
	Next  string
}

// ReconcileCandidate — место-кандидат отчёта сверки для ручной привязки.
type ReconcileCandidate struct {
	GeonameID  int64
	Name       string
	RegionName *string
	Population int64
}

// ReconcileCity — город без автоматической привязки и кандидаты.
type ReconcileCity struct {
	CityID     uuid.UUID
	Slug       string
	Candidates []ReconcileCandidate
}

// ReconcileLinked — город, привязанный сверкой.
type ReconcileLinked struct {
	CityID    uuid.UUID
	Slug      string
	GeonameID int64
}

// ReconcileNotFound — город, для которого мест не нашлось.
type ReconcileNotFound struct {
	CityID uuid.UUID
	Slug   string
}

// ReconcileReport — отчёт сверки (GeoReconcileReport).
type ReconcileReport struct {
	Linked              []ReconcileLinked
	Ambiguous, Conflict []ReconcileCity
	NotFound            []ReconcileNotFound
	Missing             []int64
	SkippedTimezone     int
}

// ImportReport — импорт с отчётом сверки; Reconciled nil — импорт идёт или упал до сверки.
type ImportReport struct {
	Import
	Reconciled *ReconcileReport
}

// New — сервис чтения поверх пула (роль api или admin: нужен только SELECT).
func New(pool *pgxpool.Pool) Service { return &reader{st: store.New(pool)} }

// ImportInProgressError — 409 geo.import_in_progress с задачей, которая уже стоит в очереди или выполняется (R26):
// JobID — id задачи River (дубль уникальности или river_job_id строки running); nil — импорт оператора (без задачи).
// errors.Is(err, ErrImportInProgress) — true.
type ImportInProgressError struct{ JobID *int64 }

func (e *ImportInProgressError) Error() string {
	if e.JobID == nil {
		return ErrImportInProgress.Error() + " (импорт оператора)"
	}
	return fmt.Sprintf("%s (задача %d)", ErrImportInProgress, *e.JobID)
}

func (e *ImportInProgressError) Is(target error) bool { return target == ErrImportInProgress }

// Состояния задачи импорта в очереди (GeoImportJob.state, R7).
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobCompleted = "completed"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// ImportJob — задача импорта в очереди (getGeoImportJob, cancelGeoImportJob). ImportID — последняя строка журнала
// задачи (nil — задача ещё не начиналась).
type ImportJob struct {
	JobID                int64
	State                string
	ImportID             *uuid.UUID
	Attempt, MaxAttempts int
	CreatedAt            time.Time
}
