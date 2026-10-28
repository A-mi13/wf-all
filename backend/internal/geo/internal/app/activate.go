package app

// Сценарии админки geo с записью (план B, Task 10–14). Активация города (activateGeoCity) — спека geo §5.3,
// §6.3 п.3, R13: город из места GeoNames, одна транзакция с аудитом и событием.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
)

const (
	// maxSlugAttempts — кандидатов slug на активацию (R13): занятые пропускаются, гонка с уникальностью — следующий.
	maxSlugAttempts = 20
	citySlugKey     = "cities_slug_key"
	// admin1Missing — у места нет кода admin1 (NULL в источнике)
	admin1Missing = ""

	actionCityActivated = "geo.city_activated"
	objectCity          = "city"
)

// RiverInserter — клиент River admin-api для сценариев импорта (Task 14): вставка в транзакции действия,
// состояние и отмена задачи. Активация его не использует.
type RiverInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	JobGet(ctx context.Context, id int64) (*rivertype.JobRow, error)
	JobCancelTx(ctx context.Context, tx pgx.Tx, jobID int64) (*rivertype.JobRow, error)
}

// AdminDeps — зависимости сценариев админки на пуле admin-api (роль admin).
type AdminDeps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	River RiverInserter
	Log   *slog.Logger
}

// Admin — сценарии админки geo с записью.
type Admin struct {
	d    AdminDeps
	hook func(ctx context.Context, stage string) // только тесты (SetStageHook)
}

// NewAdmin — сценарии на зависимостях; Log nil — журнал отбрасывается.
func NewAdmin(d AdminDeps) *Admin {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Admin{d: d}
}

// Activation — запрос активации: место GeoNames; статус ("" — waitlist), страна ("" — страна места), регион
// (nil — регион места, при переопределённой стране — без региона); Locale — язык ответа (названия карточки).
type Activation struct {
	GeonameID   int64
	Status      geo.Status
	CountryCode string
	RegionID    *uuid.UUID
	Locale      string
}

// errSlugExhausted — все кандидаты slug заняты: нарушенный инвариант (тёзок больше maxSlugAttempts) — 500 с логом.
var errSlugExhausted = errors.New("geo: кандидаты адреса города исчерпаны")

// Activate — город из места GeoNames (спека geo §5.3, R13) в одной транзакции под Idempotency-Key вызывающего:
//  1. место — нет или пропало из источника (missing_since) → geo.ErrPlaceNotFound;
//  2. страна — из запроса или места; нет или выключена → geo.ErrCountryNotEnabled; на месте уже город →
//     geo.ErrCityAlreadyActive;
//  3. регион — region_id своей страны (чужой или нет — geo.ErrRegionNotFound); без него и без переопределения
//     страны — регион по admin1 места (существующий или новый с названиями, ensureRegion); переопределена страна,
//     admin1 «00» или пуст — без региона;
//  4. названия — язык страны и en (выбор импорта по §3.4, запасное — nameFallback); cities.name — язык страны;
//  5. таймзона места проверена импортом: негодная — нарушенный инвариант, ошибка в лог и 500;
//  6. slug — ActivationSlug и SlugCandidates, занятые (cities, история) пропускаются; вставка — в savepoint:
//     23505 cities_slug_key — следующий кандидат, 23505 cities_geoname_id_key — geo.ErrCityAlreadyActive
//     (параллельная активация того же места); до maxSlugAttempts;
//  7. аудит geo.city_activated и событие geo.city_activated — в той же транзакции.
//
// Возвращает карточку нового города (как getGeoCity) из той же транзакции.
func (a *Admin) Activate(ctx context.Context, s *auth.Staff, in Activation, m approvals.Meta) (geo.AdminCity, error) {
	status := in.Status
	if status == "" {
		status = geo.StatusWaitlist
	}
	switch status {
	case geo.StatusWaitlist, geo.StatusPilot, geo.StatusLive:
	default:
		return geo.AdminCity{}, httpx.NewFieldError("body.status", "enum")
	}
	now := a.d.Clock.Now()
	var card geo.AdminCity
	err := db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		place, err := q.ActivatePlace(ctx, in.GeonameID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && place.Missing {
			return geo.ErrPlaceNotFound
		}
		if err != nil {
			return err
		}
		code := place.CountryCode
		if in.CountryCode != "" {
			code = strings.ToUpper(in.CountryCode)
		}
		overridden := code != place.CountryCode
		country, err := q.ActivateCountry(ctx, code)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !country.IsEnabled {
			return geo.ErrCountryNotEnabled
		}
		if err != nil {
			return err
		}
		if _, err := clock.In(now, place.Timezone); err != nil {
			a.d.Log.ErrorContext(ctx, "geo: таймзона места не годится — импорт пропустил негодную",
				"geoname_id", place.GeonameID, "timezone", place.Timezone, "err", err)
			return fmt.Errorf("geo: таймзона места %d: %w", place.GeonameID, err)
		}
		if found, err := q.ActivateCityOnPlace(ctx, place.GeonameID); err != nil {
			return err
		} else if found {
			return geo.ErrCityAlreadyActive
		}
		a.at(ctx, "region")
		plan, err := a.planRegion(ctx, q, in.RegionID, overridden, place, country, code)
		if err != nil {
			return err
		}
		names, err := activationNames(ctx, q, place, country.DefaultLocale)
		if err != nil {
			return err
		}
		// Порядок замков «город → регион» — как у сверки импорта (import.go, link: ImportLinkCity, затем
		// ImportLinkRegion; ревью G5 I1). Сначала город (уникальный geoname_id), регион по admin1 — после: иначе
		// активация держала бы (страна, admin1) и ждала geoname_id, а сверка — наоборот, и PostgreSQL убил бы одну
		// из них 40P01 (db.InTx его не повторяет: 500 на «Активировать» или падение импорта страны).
		cityID := id.New()
		slug, err := a.insertCity(ctx, tx, place, plan.ascii, geodb.ActivateInsertCityParams{
			ID: cityID, CountryID: country.ID, RegionID: plan.id, Name: names[country.DefaultLocale],
			Timezone: place.Timezone, Status: string(status), Now: now, GeonameID: place.GeonameID,
		})
		if err != nil {
			return err
		}
		regionID := plan.id
		if plan.admin1 != nil {
			a.at(ctx, "ensure")
			rid, err := ensureRegion(ctx, q, country.ID, code, country.DefaultLocale, place.Admin1Code, *plan.admin1, now)
			if err != nil {
				return err
			}
			if err := q.ActivateSetCityRegion(ctx, geodb.ActivateSetCityRegionParams{ID: cityID, RegionID: rid}); err != nil {
				return err
			}
			regionID = &rid
		}
		arg := geodb.ActivateCityNamesParams{CityID: cityID}
		for _, l := range namesOrder(country.DefaultLocale) {
			if n := names[l]; n != "" {
				arg.Locales, arg.Names = append(arg.Locales, l), append(arg.Names, n)
			}
		}
		if err := q.ActivateCityNames(ctx, arg); err != nil {
			return err
		}
		after := map[string]any{"geoname_id": place.GeonameID, "slug": slug, "status": status,
			"country_code": code, "region_id": regionID}
		if err := audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: s.Role, ActorLabel: s.Label, Action: actionCityActivated,
			ObjectType: objectCity, ObjectID: cityID, ScopeCityID: cityID, After: after, IP: m.IP, UserAgent: m.UserAgent,
		}); err != nil {
			return err
		}
		if _, err := events.Publish(ctx, geo.CityActivatedEvent(cityID, code, regionID, status, place.GeonameID)); err != nil {
			return err
		}
		card, err = cityCard(ctx, q, cityID, in.Locale)
		return err
	})
	if errors.Is(err, errSlugExhausted) {
		a.d.Log.ErrorContext(ctx, "geo: активация — все кандидаты адреса заняты", "geoname_id", in.GeonameID)
	}
	return card, err
}

// regionPlan — регион нового города, решённый чтениями без замков (шаг 3): id — регион из запроса (уже есть);
// admin1 — регион по коду места, который заводится (ensureRegion) только после вставки города; ascii — суффикс slug.
type regionPlan struct {
	id     *uuid.UUID
	admin1 *geodb.ActivateAdmin1Row
	ascii  string
}

// planRegion — регион нового города (шаг 3) только чтениями: region_id запроса — своей страны (иначе
// geo.ErrRegionNotFound); без него и без переопределения страны — admin1 места; переопределена страна, admin1 «00»
// или пуст — без региона.
func (a *Admin) planRegion(ctx context.Context, q *geodb.Queries, requested *uuid.UUID, overridden bool,
	place geodb.ActivatePlaceRow, country geodb.ActivateCountryRow, code string,
) (regionPlan, error) {
	if requested != nil {
		r, err := q.ActivateRegionOfCountry(ctx, geodb.ActivateRegionOfCountryParams{ID: *requested, CountryID: country.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return regionPlan{}, geo.ErrRegionNotFound
		}
		if err != nil {
			return regionPlan{}, err
		}
		return regionPlan{id: &r.ID, ascii: r.AsciiName}, nil
	}
	if overridden || place.Admin1Code == admin1Missing || place.Admin1Code == admin1Unknown {
		return regionPlan{}, nil
	}
	a1, err := q.ActivateAdmin1(ctx, geodb.ActivateAdmin1Params{Locale: country.DefaultLocale, CountryCode: code,
		Admin1Code: place.Admin1Code})
	if errors.Is(err, pgx.ErrNoRows) {
		// импорт проверяет, что admin1 мест есть в admin1 страны (R34): без строки — регион не ставится
		a.d.Log.WarnContext(ctx, "geo: активация — нет admin1 места, город без региона",
			"geoname_id", place.GeonameID, "admin1", place.Admin1Code)
		return regionPlan{}, nil
	}
	if err != nil {
		return regionPlan{}, err
	}
	return regionPlan{admin1: &a1, ascii: a1.AsciiName}, nil
}

// ensureRegion — регион страны с кодом admin1: существующий или новый с названиями по §3.4 (строка языка
// страны — каноническое имя, прочие — из admin1 источника). Гонка двух активаций одного нового региона: вторая
// вставка ждёт первую и после её коммита не вставляет ничего (ON CONFLICT DO NOTHING по
// regions_country_admin1_key) — регион перечитывается, транзакция не ломается 23505.
func ensureRegion(ctx context.Context, q *geodb.Queries, countryID uuid.UUID, code, countryLocale, admin1 string,
	a1 geodb.ActivateAdmin1Row, now time.Time,
) (uuid.UUID, error) {
	name := strings.TrimSpace(a1.LocalName)
	if name == "" {
		name = a1.AsciiName
	}
	rid, err := q.ActivateInsertRegion(ctx, geodb.ActivateInsertRegionParams{
		ID: id.New(), CountryID: countryID, Name: name, Admin1Code: admin1, CreatedAt: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return q.ActivateRegionByAdmin1(ctx, geodb.ActivateRegionByAdmin1Params{CountryID: countryID, Admin1Code: admin1})
	}
	if err != nil {
		return uuid.Nil, err
	}
	return rid, q.ActivateRegionNames(ctx, geodb.ActivateRegionNamesParams{
		RegionID: rid, CountryLocale: countryLocale, Canonical: name, CountryCode: code, Admin1Code: admin1})
}

// namesOrder — языки названий нового города: язык страны и en (без повтора).
func namesOrder(countryLocale string) []string {
	if countryLocale == "en" {
		return []string{"en"}
	}
	return []string{countryLocale, "en"}
}

// activationNames — названия нового города (шаг 4): строки места, выбранные импортом по §3.4; нет строки —
// запасное шага 7 (nameFallback: name GeoNames для языка страны, ascii_name для en).
func activationNames(ctx context.Context, q *geodb.Queries, place geodb.ActivatePlaceRow, countryLocale string) (map[string]string, error) {
	rows, err := q.ActivatePlaceNames(ctx, place.GeonameID)
	if err != nil {
		return nil, err
	}
	have := map[string]string{}
	for _, r := range rows {
		have[r.Locale] = strings.TrimSpace(r.Name)
	}
	out := map[string]string{}
	for _, l := range namesOrder(countryLocale) {
		n := have[l]
		if n == "" {
			n = strings.TrimSpace(nameFallback(l, countryLocale, place.Name, place.AsciiName))
		}
		out[l] = n
	}
	return out, nil
}

// insertCity — вставка города по кандидатам slug (шаг 6): занятые пропускаются, вставка — в savepoint, чтобы
// 23505 не убил транзакцию (25P02). Возвращает выбранный slug.
func (a *Admin) insertCity(ctx context.Context, tx pgx.Tx, place geodb.ActivatePlaceRow, regionASCII string,
	arg geodb.ActivateInsertCityParams,
) (string, error) {
	q := geodb.New(tx)
	tried := 0
	for cand := range domain.SlugCandidates(domain.ActivationSlug(place.AsciiName, place.GeonameID), regionASCII) {
		if tried == maxSlugAttempts {
			break
		}
		tried++
		taken, err := q.SlugTaken(ctx, cand)
		if err != nil {
			return "", err
		}
		if taken {
			continue
		}
		a.at(ctx, "insert")
		arg.Slug = cand
		ok, err := insertCitySavepoint(ctx, tx, arg)
		if err != nil {
			return "", err
		}
		if ok {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%w: место %s, попыток %d", errSlugExhausted, strconv.FormatInt(place.GeonameID, 10), tried)
}

// insertCitySavepoint — одна попытка вставки: false — slug заняли параллельно (23505 cities_slug_key, откат до
// savepoint); место заняли параллельно (23505 cities_geoname_id_key) — geo.ErrCityAlreadyActive; место удалил
// параллельный импорт (вставлено 0 строк) — geo.ErrPlaceNotFound.
func insertCitySavepoint(ctx context.Context, tx pgx.Tx, arg geodb.ActivateInsertCityParams) (bool, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}
	n, err := geodb.New(sp).ActivateInsertCity(ctx, arg)
	if err == nil {
		if n == 0 {
			return false, errors.Join(geo.ErrPlaceNotFound, sp.Rollback(ctx))
		}
		return true, sp.Commit(ctx)
	}
	if rerr := sp.Rollback(ctx); rerr != nil {
		return false, errors.Join(err, rerr)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeUniqueViolation {
		switch pgErr.ConstraintName {
		case citySlugKey:
			return false, nil
		case cityGeonameKey:
			return false, geo.ErrCityAlreadyActive
		}
	}
	return false, err
}

// at — точка хука тестов гонок (SetStageHook); в проде hook nil.
func (a *Admin) at(ctx context.Context, stage string) {
	if a.hook != nil {
		a.hook(ctx, stage)
	}
}
