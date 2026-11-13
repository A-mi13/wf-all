package app

// Чтения админки geo (план B, Task 9; спека сотрудников §6, Приложения 1–2 плана): все статусы и страны (в
// том числе выключенные), названия по всем языкам, история адресов, журнал импортов с подписью автора из
// аудита постановки (R3). Скоуп модератора — у вызывающего (geo/admin): сюда приходит уже суженный фильтр.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/page"
)

// Наборы полей курсоров (page.Keyset): у списка городов админки — свой Set, курсор публичного списка
// (geo.cities(...)) сюда не подходит и наоборот (порядок тот же, фильтры — нет).
const (
	placesSet      = "geo.places(population,geoname_id)"
	adminCitiesSet = "geo.admin_cities(rank,population,id)"
	importsSet     = "geo.imports(started_at,id)"

	// importRequestedAction, importJobObject — запись аудита постановки импорта (R3, Task 14): по ней
	// журнал подписывает строку «кто поставил».
	importRequestedAction = "geo.import_requested"
	importJobObject       = "geonames_import_job"
	// auditPage — записей журнала за запрос подписей (предел audit.List): больше — следующая страница.
	auditPage = 100
)

var (
	placesKinds      = []page.Kind{page.KindInt64, page.KindInt64}
	adminCitiesKinds = []page.Kind{page.KindInt64, page.KindInt64, page.KindUUID}
	importsKinds     = []page.Kind{page.KindTime, page.KindUUID}
)

// AdminReader — чтения админки поверх пула admin-api (роль admin: SELECT справочника, источника, журнала
// импортов и audit_log).
type AdminReader struct{ pool *pgxpool.Pool }

// NewAdminReader — чтения админки на пуле.
func NewAdminReader(pool *pgxpool.Pool) *AdminReader { return &AdminReader{pool: pool} }

// nameLocale — локаль названий: поддерживаемая — она же, иначе "" (канонические названия на языке страны
// записи; строк с пустой локалью нет — CHECK ^[a-z]{2}$).
func nameLocale(locale string) string {
	if domain.LocaleSupported(locale) {
		return locale
	}
	return ""
}

// normalizeQ — q через normalize_text базы; пусто после нормализации — geo.ErrBadQuery (400 query.q).
func normalizeQ(ctx context.Context, q *geodb.Queries, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	n, err := q.NormalizeQuery(ctx, raw)
	if err != nil {
		return "", err
	}
	if n == "" {
		return "", geo.ErrBadQuery
	}
	return n, nil
}

// optString — "" как nil (NULL контракта).
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Places — места GeoNames для активации (listGeoPlaces).
func (r *AdminReader) Places(ctx context.Context, p geo.PlaceQuery) (geo.PlacePage, error) {
	q := geodb.New(r.pool)
	arg := geodb.AdminListPlacesParams{Locale: nameLocale(p.Locale), Country: strings.ToUpper(p.Country)}
	var err error
	if arg.QPrefix, err = normalizeQ(ctx, q, p.Q); err != nil {
		return geo.PlacePage{}, err
	}
	if p.Cursor != "" {
		k, err := page.DecodeKeyset(p.Cursor, placesSet, placesKinds...)
		if err != nil {
			return geo.PlacePage{}, err
		}
		arg.HasCursor, arg.AfterPopulation, arg.AfterGeonameID = true, k.Values[0].(int64), k.Values[1].(int64)
	}
	limit := page.Limit(p.Limit)
	arg.RowLimit = int64(limit) + 1
	rows, err := q.AdminListPlaces(ctx, arg)
	if err != nil {
		return geo.PlacePage{}, err
	}
	out := geo.PlacePage{Items: make([]geo.AdminPlace, 0, min(len(rows), limit))}
	for _, row := range rows[:min(len(rows), limit)] {
		out.Items = append(out.Items, geo.AdminPlace{
			GeonameID: row.GeonameID, Name: row.Name, ASCIIName: row.AsciiName, FeatureCode: row.FeatureCode,
			Population: row.Population, Timezone: row.Timezone, RegionName: optString(row.RegionName),
			Country: geo.Country{Code: row.CountryCode, Name: row.CountryName}, CityID: row.CityID,
		})
	}
	if len(rows) > limit {
		last := rows[limit-1]
		out.Next = page.EncodeKeyset(page.Keyset{Set: placesSet, Values: []any{last.Population, last.GeonameID}}, placesKinds...)
	}
	return out, nil
}

// Cities — города админки (listGeoCities): все статусы и страны; IDs уже сужены скоупом вызывающего.
func (r *AdminReader) Cities(ctx context.Context, p geo.AdminCityQuery) (geo.AdminCityPage, error) {
	q := geodb.New(r.pool)
	arg := geodb.AdminListCitiesParams{
		Locale:   nameLocale(p.Locale),
		Statuses: make([]string, 0, len(p.Status)),
		Country:  strings.ToUpper(p.Country),
	}
	for _, s := range p.Status {
		arg.Statuses = append(arg.Statuses, string(s))
	}
	if p.RegionID != nil {
		arg.HasRegion, arg.RegionID = true, *p.RegionID
	}
	// nil — без фильтра; не nil, даже пустой, — только эти города (пустой — ни одного, M10)
	if p.IDs != nil {
		arg.FilterIds, arg.Ids = true, p.IDs
	}
	var err error
	if arg.QPrefix, err = normalizeQ(ctx, q, p.Q); err != nil {
		return geo.AdminCityPage{}, err
	}
	if p.Cursor != "" {
		k, err := page.DecodeKeyset(p.Cursor, adminCitiesSet, adminCitiesKinds...)
		if err != nil {
			return geo.AdminCityPage{}, err
		}
		arg.HasCursor, arg.AfterRank, arg.AfterPopulation, arg.AfterID =
			true, k.Values[0].(int64), k.Values[1].(int64), k.Values[2].(uuid.UUID)
	}
	limit := page.Limit(p.Limit)
	arg.RowLimit = int64(limit) + 1
	rows, err := q.AdminListCities(ctx, arg)
	if err != nil {
		return geo.AdminCityPage{}, err
	}
	out := geo.AdminCityPage{Items: make([]geo.AdminCityItem, 0, min(len(rows), limit))}
	for _, row := range rows[:min(len(rows), limit)] {
		c := geo.AdminCityItem{
			ID: row.ID, Slug: row.Slug, Name: row.Name, Status: geo.Status(row.Status),
			CountryCode: row.CountryCode, CountryEnabled: row.CountryEnabled, GeonameID: row.GeonameID,
			Population: row.Population, DistrictsCount: int(row.DistrictsCount),
		}
		if row.RegionID != nil {
			c.Region = &geo.Ref{ID: *row.RegionID, Name: row.RegionName}
		}
		out.Items = append(out.Items, c)
	}
	if len(rows) > limit {
		last := rows[limit-1]
		out.Next = page.EncodeKeyset(page.Keyset{Set: adminCitiesSet,
			Values: []any{last.StatusRank, last.Population, last.ID}}, adminCitiesKinds...)
	}
	return out, nil
}

// readTx — согласованный снимок нескольких запросов карточки: только чтение, REPEATABLE READ (город,
// названия и история — из одного момента, а не вперемешку с параллельной правкой).
func (r *AdminReader) readTx(ctx context.Context, fn func(q *geodb.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(geodb.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// City — карточка города (getGeoCity): страна любая, в том числе выключенная; нет города — ErrCityNotFound.
func (r *AdminReader) City(ctx context.Context, cityID uuid.UUID, locale string) (geo.AdminCity, error) {
	var out geo.AdminCity
	err := r.readTx(ctx, func(q *geodb.Queries) error {
		var err error
		out, err = cityCard(ctx, q, cityID, locale)
		return err
	})
	return out, err
}

// cityCard — карточка города в транзакции q (чтение, активация и правка отвечают одной формой).
func cityCard(ctx context.Context, q *geodb.Queries, cityID uuid.UUID, locale string) (geo.AdminCity, error) {
	row, err := q.AdminGetCity(ctx, geodb.AdminGetCityParams{Locale: nameLocale(locale), ID: cityID})
	if errors.Is(err, pgx.ErrNoRows) {
		return geo.AdminCity{}, geo.ErrCityNotFound
	}
	if err != nil {
		return geo.AdminCity{}, err
	}
	names, err := q.AdminCityNames(ctx, cityID)
	if err != nil {
		return geo.AdminCity{}, err
	}
	hist, err := q.AdminCitySlugHistory(ctx, cityID)
	if err != nil {
		return geo.AdminCity{}, err
	}
	c := geo.AdminCity{
		ID: row.ID, Slug: row.Slug, SlugHistory: make([]geo.SlugEntry, 0, len(hist)), Name: row.Name,
		Names: make(map[string]string, len(names)), CanonicalLocale: row.CanonicalLocale,
		Status: geo.Status(row.Status), GeonameID: row.GeonameID, Population: row.Population,
		Timezone: row.Timezone, Location: geo.Point{Lat: row.Lat, Lon: row.Lon},
		Country:        geo.CityCountry{Code: row.CountryCode, Name: row.CountryName, IsEnabled: row.CountryEnabled},
		DistrictsCount: int(row.DistrictsCount), Version: row.Version,
	}
	for _, n := range names {
		c.Names[n.Locale] = n.Name
	}
	for _, h := range hist {
		c.SlugHistory = append(c.SlugHistory, geo.SlugEntry{Slug: h.Slug, ReplacedAt: h.ReplacedAt})
	}
	if row.RegionID != nil {
		c.Region = &geo.Ref{ID: *row.RegionID, Name: row.RegionName}
	}
	return c, nil
}

// Districts — районы города включая архивные (listGeoDistricts); нет города — ErrCityNotFound.
func (r *AdminReader) Districts(ctx context.Context, cityID uuid.UUID) ([]geo.AdminDistrict, error) {
	var out []geo.AdminDistrict
	err := r.readTx(ctx, func(q *geodb.Queries) error {
		found, err := q.AdminCityExists(ctx, cityID)
		if err != nil {
			return err
		}
		if !found {
			return geo.ErrCityNotFound
		}
		rows, err := q.AdminListDistricts(ctx, cityID)
		if err != nil {
			return err
		}
		out = make([]geo.AdminDistrict, 0, len(rows))
		for _, d := range rows {
			out = append(out, geo.AdminDistrict{ID: d.ID, Name: d.Name, ArchivedAt: d.ArchivedAt, Version: d.Version})
		}
		return nil
	})
	return out, err
}

// Countries — все страны с правилами и названиями (listGeoCountries).
func (r *AdminReader) Countries(ctx context.Context, locale string) ([]geo.AdminCountry, error) {
	var out []geo.AdminCountry
	err := r.readTx(ctx, func(q *geodb.Queries) error {
		rows, err := q.AdminListCountries(ctx, nameLocale(locale))
		if err != nil {
			return err
		}
		names, err := q.AdminCountryNames(ctx)
		if err != nil {
			return err
		}
		byCode := map[string]map[string]string{}
		for _, n := range names {
			if byCode[n.Code] == nil {
				byCode[n.Code] = map[string]string{}
			}
			byCode[n.Code][n.Locale] = n.Name
		}
		out = make([]geo.AdminCountry, 0, len(rows))
		for _, c := range rows {
			nm := byCode[c.Code]
			if nm == nil {
				nm = map[string]string{}
			}
			out = append(out, geo.AdminCountry{
				Code: c.Code, Name: c.Name, Names: nm, Currency: c.Currency, DefaultLocale: c.DefaultLocale,
				PhonePrefix: c.PhonePrefix, WeekStartsOn: int(c.WeekStartsOn), MinSignupAge: int(c.MinSignupAge),
				AgeOfMajority: int(c.AgeOfMajority), IsEnabled: c.IsEnabled, OpenCities: int(c.OpenCities),
				Version: c.Version,
			})
		}
		return nil
	})
	return out, err
}

// Regions — регионы страны (listGeoRegions); нет страны — ErrCountryNotFound.
func (r *AdminReader) Regions(ctx context.Context, code, locale string) ([]geo.AdminRegion, error) {
	var out []geo.AdminRegion
	err := r.readTx(ctx, func(q *geodb.Queries) error {
		found, err := q.AdminCountryExists(ctx, code)
		if err != nil {
			return err
		}
		if !found {
			return geo.ErrCountryNotFound
		}
		rows, err := q.AdminListRegions(ctx, geodb.AdminListRegionsParams{Locale: nameLocale(locale), Code: code})
		if err != nil {
			return err
		}
		names, err := q.AdminRegionNames(ctx, code)
		if err != nil {
			return err
		}
		byRegion := map[uuid.UUID]map[string]string{}
		for _, n := range names {
			if byRegion[n.RegionID] == nil {
				byRegion[n.RegionID] = map[string]string{}
			}
			byRegion[n.RegionID][n.Locale] = n.Name
		}
		out = make([]geo.AdminRegion, 0, len(rows))
		for _, rg := range rows {
			nm := byRegion[rg.ID]
			if nm == nil {
				nm = map[string]string{}
			}
			out = append(out, geo.AdminRegion{ID: rg.ID, Name: rg.Name, Names: nm, Admin1Code: rg.GeonameAdmin1Code})
		}
		return nil
	})
	return out, err
}

// Imports — журнал импортов, новые сверху (listGeoImports), с подписью «кто поставил» (R3).
func (r *AdminReader) Imports(ctx context.Context, p geo.ImportQuery) (geo.ImportPage, error) {
	arg := geodb.AdminListImportsParams{Country: strings.ToUpper(p.Country)}
	if p.JobID != nil {
		arg.HasJob, arg.JobID = true, *p.JobID
	}
	if p.Cursor != "" {
		k, err := page.DecodeKeyset(p.Cursor, importsSet, importsKinds...)
		if err != nil {
			return geo.ImportPage{}, err
		}
		arg.HasCursor, arg.AfterStartedAt, arg.AfterID = true, k.Values[0].(time.Time), k.Values[1].(uuid.UUID)
	}
	limit := page.Limit(p.Limit)
	arg.RowLimit = int64(limit) + 1
	rows, err := geodb.New(r.pool).AdminListImports(ctx, arg)
	if err != nil {
		return geo.ImportPage{}, err
	}
	out := geo.ImportPage{Items: make([]geo.Import, 0, min(len(rows), limit))}
	for _, row := range rows[:min(len(rows), limit)] {
		out.Items = append(out.Items, geo.Import{
			ID: row.ID, CountryCode: row.CountryCode, Status: row.Status, StartedAt: row.StartedAt,
			FinishedAt: row.FinishedAt, StartedBy: row.StartedBy, RiverJobID: row.RiverJobID,
			PlacesUpserted: intPtr(row.PlacesUpserted), PlacesRemoved: intPtr(row.PlacesRemoved),
			PlacesMissing: intPtr(row.PlacesMissing), NamesUpserted: intPtr(row.NamesUpserted), Error: row.Error,
		})
	}
	if err := r.labelImports(ctx, out.Items); err != nil {
		return geo.ImportPage{}, err
	}
	if len(rows) > limit {
		last := rows[limit-1]
		out.Next = page.EncodeKeyset(page.Keyset{Set: importsSet, Values: []any{last.StartedAt, last.ID}}, importsKinds...)
	}
	return out, nil
}

// Import — строка журнала с отчётом сверки (getGeoImport); названия кандидатов — на locale; нет строки —
// ErrImportNotFound. Кандидат, которого уже нет среди мест (удалён следующим импортом), в ответ не попадает:
// привязать к нему нельзя (linkGeoCity ответил бы geo.place_not_found).
func (r *AdminReader) Import(ctx context.Context, importID uuid.UUID, locale string) (geo.ImportReport, error) {
	q := geodb.New(r.pool)
	row, err := q.AdminGetImport(ctx, importID)
	if errors.Is(err, pgx.ErrNoRows) {
		return geo.ImportReport{}, geo.ErrImportNotFound
	}
	if err != nil {
		return geo.ImportReport{}, err
	}
	out := geo.ImportReport{Import: geo.Import{
		ID: row.ID, CountryCode: row.CountryCode, Status: row.Status, StartedAt: row.StartedAt,
		FinishedAt: row.FinishedAt, StartedBy: row.StartedBy, RiverJobID: row.RiverJobID,
		PlacesUpserted: intPtr(row.PlacesUpserted), PlacesRemoved: intPtr(row.PlacesRemoved),
		PlacesMissing: intPtr(row.PlacesMissing), NamesUpserted: intPtr(row.NamesUpserted), Error: row.Error,
	}}
	one := []geo.Import{out.Import}
	if err := r.labelImports(ctx, one); err != nil {
		return geo.ImportReport{}, err
	}
	out.Import = one[0]
	if len(row.Reconciled) == 0 {
		return out, nil
	}
	var rep report
	if err := json.Unmarshal(row.Reconciled, &rep); err != nil {
		return geo.ImportReport{}, fmt.Errorf("geo: отчёт сверки импорта %s: %w", importID, err)
	}
	if out.Reconciled, err = reportOf(ctx, q, rep, nameLocale(locale)); err != nil {
		return geo.ImportReport{}, err
	}
	return out, nil
}

// reportOf — отчёт сверки для ответа: кандидаты ambiguous и conflict с названием, регионом и населением.
func reportOf(ctx context.Context, q *geodb.Queries, rep report, locale string) (*geo.ReconcileReport, error) {
	var ids []int64
	for _, c := range slices.Concat(rep.Ambiguous, rep.Conflict) {
		ids = append(ids, c.Candidates...)
	}
	places := map[int64]geo.ReconcileCandidate{}
	if len(ids) > 0 {
		rows, err := q.AdminPlacesByIDs(ctx, geodb.AdminPlacesByIDsParams{Locale: locale, Ids: ids})
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			places[p.GeonameID] = geo.ReconcileCandidate{GeonameID: p.GeonameID, Name: p.Name,
				RegionName: optString(p.RegionName), Population: p.Population}
		}
	}
	unmatched := func(list []UnmatchedCity) []geo.ReconcileCity {
		out := make([]geo.ReconcileCity, 0, len(list))
		for _, c := range list {
			rc := geo.ReconcileCity{CityID: c.CityID, Slug: c.Slug, Candidates: []geo.ReconcileCandidate{}}
			for _, id := range c.Candidates {
				if p, ok := places[id]; ok {
					rc.Candidates = append(rc.Candidates, p)
				}
			}
			out = append(out, rc)
		}
		return out
	}
	out := &geo.ReconcileReport{
		Linked: make([]geo.ReconcileLinked, 0, len(rep.Linked)), Ambiguous: unmatched(rep.Ambiguous),
		Conflict: unmatched(rep.Conflict), NotFound: make([]geo.ReconcileNotFound, 0, len(rep.NotFound)),
		Missing: append(make([]int64, 0, len(rep.Missing)), rep.Missing...), SkippedTimezone: rep.SkippedTimezone,
	}
	for _, l := range rep.Linked {
		out.Linked = append(out.Linked, geo.ReconcileLinked{CityID: l.CityID, Slug: l.Slug, GeonameID: l.GeonameID})
	}
	for _, n := range rep.NotFound {
		out.NotFound = append(out.NotFound, geo.ReconcileNotFound{CityID: n.CityID, Slug: n.Slug})
	}
	return out, nil
}

// labelClockSkew — допуск расхождения часов в сопоставлении подписи (m1 ревью G5) и строки журнала с задачей River
// (ImportLatestByJob, ревью G7 m5: created_at задачи — время базы): created_at записи постановки —
// время базы (now() транзакции admin-api, как у всех записей журнала), started_at строки — часы воркера (правило
// clock.Clock, другой хост). Схему и источник времени журнала не меняем: сравниваются два источника, поэтому с
// допуском; настоящий повтор river_job.id (сброс последовательности River) отстоит на дни, а не на минуту.
const labelClockSkew = time.Minute

// labelImports — started_by_label строк из записей аудита постановки geo.import_requested (R3): один запрос
// журнала по объектам id.Named(geonames_import_job, <job_id>) строк с river_job_id (страница — до auditPage
// записей, больше — следующие страницы). Строке — новейшая запись её задачи с created_at ≤ started_at строки +
// labelClockSkew: после сброса последовательности river_job.id повторится, и без привязки ко времени подпись
// досталась бы чужому импорту. Оператор (river_job_id нет) и строка без записи — nil (экран: «сотрудник»).
func (r *AdminReader) labelImports(ctx context.Context, items []geo.Import) error {
	objects := map[uuid.UUID]bool{}
	var latest time.Time
	for _, it := range items {
		if it.RiverJobID == nil {
			continue
		}
		objects[importJobID(*it.RiverJobID)] = true
		if it.StartedAt.After(latest) {
			latest = it.StartedAt
		}
	}
	if len(objects) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(objects))
	for o := range objects {
		ids = append(ids, o)
	}
	// записи новые сверху; To — исключительно, поэтому +1 мкс (точность timestamptz)
	p := audit.ListParams{ActionPrefix: importRequestedAction, ObjectType: importJobObject, ObjectIDs: ids,
		To: latest.Add(labelClockSkew + time.Microsecond), Limit: auditPage}
	byObject := map[uuid.UUID][]audit.Record{}
	for {
		recs, next, err := audit.List(ctx, r.pool, p)
		if err != nil {
			return err
		}
		for _, rec := range recs {
			byObject[rec.ObjectID] = append(byObject[rec.ObjectID], rec)
		}
		if next == "" {
			break
		}
		p.Cursor = next
	}
	for i := range items {
		it := &items[i]
		if it.RiverJobID == nil {
			continue
		}
		// записи объекта — новые сверху: первая не позже начала строки — новейшая подходящая
		for _, rec := range byObject[importJobID(*it.RiverJobID)] {
			if !rec.CreatedAt.After(it.StartedAt.Add(labelClockSkew)) {
				if rec.ActorLabel != "" {
					label := rec.ActorLabel
					it.StartedByLabel = &label
				}
				break
			}
		}
	}
	return nil
}

// importJobID — объект аудита задачи импорта (R18): детерминированный uuid от job_id River.
func importJobID(jobID int64) uuid.UUID {
	return id.Named(importJobObject, fmt.Sprint(jobID))
}

// intPtr — *int32 колонки как *int контракта.
func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}
