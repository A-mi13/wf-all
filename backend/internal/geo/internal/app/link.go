package app

// Привязка города к месту GeoNames — одна для сверки импорта (import.go, reconcile) и ручной привязки админки
// (linkGeoCity, Task 11, R14): geoname_id, version + 1, недостающие переводы города и региона, код admin1 региона,
// событие geo.city_linked; точка и население города не меняются.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
)

const (
	actionCityLinked = "geo.city_linked"
	regionAdmin1Key  = "regions_country_admin1_key"
)

// linkInput — что привязывается: город, его регион (nil — без региона), место и его код admin1 (nil — нет), страна
// города и её язык (строку этого языка привязка не создаёт — она источник канонического имени, §3.3).
type linkInput struct {
	CityID        uuid.UUID
	RegionID      *uuid.UUID
	GeonameID     int64
	Admin1        *string
	Country       string
	CountryLocale string
}

// linkCity — привязка в транзакции tx (события — через ctx той же транзакции). Возвращает версию города после
// привязки. Ошибки ImportLinkCity — как есть: pgx.ErrNoRows — город уже привязан (geoname_id не NULL), 23505
// cities_geoname_id_key — место у другого города; что с ними делать, решает вызывающий (сверка — conflict, ручная
// привязка — коды контракта).
//
// Регион (ревью G6 I1): код admin1 — только годный (admin1Known: не пустой и не «00», как у активации); переводы
// admin1 — только региону, у которого в итоге именно этот код (ImportRegionNamesFromAdmin1 сверяет код в той же
// инструкции): регион с другим кодом или код, отданный параллельной активации, переводов чужого admin1 не получает.
//
// Порядок записей (ревью G6 I2): код региона (regions_country_admin1_key) — до geoname_id города
// (cities_geoname_id_key), как у активации (ensureRegion, затем вставка города). Активация и ручная привязка держат
// один разделяемый замок страны и идут параллельно: при встречном порядке привязка держала бы место и ждала регион
// активации, а активация — место привязки (40P01). С общим порядком привязка ждёт регион до записи места, затем
// 23505 cities_geoname_id_key → geo.ErrCityAlreadyActive. Замки строк: город (FOR UPDATE у вызывающего), регион
// (NO KEY UPDATE: индекс кода частичный — не ключ для FK, вставки городов с этим регионом не ждут). Сверка держит
// замок страны эксклюзивно — ей порядок безразличен.
func linkCity(ctx context.Context, tx pgx.Tx, in linkInput) (int64, error) {
	q := geodb.New(tx)
	region := in.RegionID != nil && in.Admin1 != nil && admin1Known(*in.Admin1)
	if region {
		if err := linkRegionCode(ctx, tx, *in.RegionID, *in.Admin1); err != nil {
			return 0, err
		}
	}
	version, err := q.ImportLinkCity(ctx, geodb.ImportLinkCityParams{ID: in.CityID, GeonameID: in.GeonameID})
	if err != nil {
		return 0, err
	}
	if _, err := q.ImportCityNamesFromPlace(ctx, geodb.ImportCityNamesFromPlaceParams{
		CityID: in.CityID, GeonameID: in.GeonameID, CountryLocale: in.CountryLocale}); err != nil {
		return 0, fmt.Errorf("geo: переводы города: %w", err)
	}
	if region {
		if _, err := q.ImportRegionNamesFromAdmin1(ctx, geodb.ImportRegionNamesFromAdmin1Params{
			RegionID: *in.RegionID, CountryCode: in.Country, Admin1Code: *in.Admin1, CountryLocale: in.CountryLocale}); err != nil {
			return 0, fmt.Errorf("geo: переводы региона: %w", err)
		}
	}
	if _, err := events.Publish(ctx, geo.CityLinkedEvent(in.CityID, version, in.GeonameID)); err != nil {
		return 0, err
	}
	return version, nil
}

// linkRegionCode — региону код admin1, если у него кода нет и код не занят другим регионом страны (ImportLinkRegion).
// Код заняли параллельно — активация того же admin1 (у неё тот же разделяемый замок страны) вставила регион после
// проверки NOT EXISTS: 23505 regions_country_admin1_key откатывает только этот savepoint, регион остаётся без кода
// (как 0 строк у ImportLinkRegion), привязка продолжается.
func linkRegionCode(ctx context.Context, tx pgx.Tx, regionID uuid.UUID, admin1 string) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("geo: savepoint кода региона: %w", err)
	}
	_, err = geodb.New(sp).ImportLinkRegion(ctx, geodb.ImportLinkRegionParams{ID: regionID, Admin1Code: admin1})
	if err == nil {
		return sp.Commit(ctx)
	}
	if rerr := sp.Rollback(ctx); rerr != nil {
		return errors.Join(err, rerr)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeUniqueViolation &&
		pgErr.ConstraintName == regionAdmin1Key {
		return nil
	}
	return fmt.Errorf("geo: код региона: %w", err)
}

// LinkCity — POST /v1/geo/cities/{cityId}/link: ручная привязка города без geoname_id к месту (кандидат отчёта
// сверки) одной транзакцией под Idempotency-Key вызывающего:
//  1. право geo.cities.manage в городе (конвейер проверил; здесь — защита в глубину);
//  2. страна города без замка, затем разделяемый advisory-замок страны первым замком (ревью G5 I1-r1: сверка
//     держит его эксклюзивно и привязывает все города страны в одной транзакции — встречный порядок «город →
//     регион» дал бы 40P01), затем город FOR UPDATE; нет — geo.ErrCityNotFound; уже привязан — geo.ErrCityAlreadyLinked;
//  3. место читается после замка: нет, пропало (missing_since) или другой страны, чем город, — geo.ErrPlaceNotFound
//     (страна замка — страна города, m-r5: разойтись с местом она не может); на месте уже город —
//     geo.ErrCityAlreadyActive (и 23505 cities_geoname_id_key при гонке);
//  4. linkCity, аудит geo.city_linked — в той же транзакции.
func (a *Admin) LinkCity(ctx context.Context, s *auth.Staff, id uuid.UUID, geonameID int64, locale string, m approvals.Meta) (geo.AdminCity, error) {
	if err := a.authorize(ctx, s, id, permCitiesManage); err != nil {
		return geo.AdminCity{}, err
	}
	var card geo.AdminCity
	err := db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		code, err := q.CityCountryCode(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return geo.ErrCityNotFound
		}
		if err != nil {
			return err
		}
		if err := lockCountries(ctx, q, code); err != nil {
			return err
		}
		row, err := q.CityForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return geo.ErrCityNotFound
		}
		if err != nil {
			return err
		}
		if row.GeonameID != nil {
			return geo.ErrCityAlreadyLinked
		}
		place, err := q.ActivatePlace(ctx, geonameID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (place.Missing || place.CountryCode != row.CountryCode) {
			return geo.ErrPlaceNotFound
		}
		if err != nil {
			return err
		}
		if taken, err := q.ActivateCityOnPlace(ctx, geonameID); err != nil {
			return err
		} else if taken {
			return geo.ErrCityAlreadyActive
		}
		a.at(ctx, "link")
		in := linkInput{CityID: id, RegionID: row.RegionID, GeonameID: geonameID, Country: row.CountryCode,
			CountryLocale: row.DefaultLocale}
		in.Admin1 = &place.Admin1Code // пустой и «00» отсекает linkCity (admin1Known)
		if _, err := linkCity(ctx, tx, in); err != nil {
			return linkProblem(err)
		}
		if err := audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: s.Role, ActorLabel: s.Label, Action: actionCityLinked,
			ObjectType: objectCity, ObjectID: id, ScopeCityID: id, After: map[string]any{"geoname_id": geonameID},
			IP: m.IP, UserAgent: m.UserAgent,
		}); err != nil {
			return err
		}
		card, err = cityCard(ctx, q, id, locale)
		return err
	})
	return card, err
}

// linkProblem — гонки ручной привязки: место заняли параллельно (23505 cities_geoname_id_key) —
// geo.ErrCityAlreadyActive; город привязан (0 строк ImportLinkCity — под FOR UPDATE недостижимо) —
// geo.ErrCityAlreadyLinked.
func linkProblem(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeUniqueViolation &&
		pgErr.ConstraintName == cityGeonameKey {
		return geo.ErrCityAlreadyActive
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return geo.ErrCityAlreadyLinked
	}
	return err
}
