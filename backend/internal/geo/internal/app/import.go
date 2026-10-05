package app

// Импорт GeoNames (спека geo §5.4): журнал geonames_imports, загрузка (source.Fetch), отбор и
// выбор названий (domain), запись пачками в одной транзакции, сверка заведённых городов, итог,
// событие geo.import_finished и аудит. Зовут задача River geo.import и команда оператора
// worker geo import (пакет jobs).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/geo/internal/source"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
)

var (
	ErrImportInProgress = errors.New("geo: импорт страны уже идёт")
	ErrCountryNotFound  = errors.New("geo: страны нет в справочнике")
	// ErrSourceTruncated — мест в источнике меньше 90 % прошлого успешного импорта: файл обрезан
	// или подменён; ничего не записано и не удалено (§5.4 шаг 4).
	ErrSourceTruncated = errors.New("geo: в источнике меньше 90 % мест прошлого успешного импорта")
	// ErrAdmin1Incomplete — отобранные места ссылаются на коды admin1, которых нет в admin1 страны
	// из источника (общий admin1CodesASCII.txt обрезан или подменён); ничего не записано (R34).
	ErrAdmin1Incomplete = errors.New("geo: в admin1 нет кодов, на которые ссылаются места")
	// ErrImportClosed — строку журнала закрыл другой запуск (счёл зависшей), пока шёл этот.
	ErrImportClosed = errors.New("geo: строка журнала импорта уже закрыта другим запуском")
)

const (
	// ImportTimeout — крайний срок задачи River geo.import (jobs.ImportWorker.Timeout).
	ImportTimeout = 30 * time.Minute
	// StaleAfter — строка running старше — процесс убит без итога (таймаут задачи + 10 минут).
	StaleAfter = ImportTimeout + 10*time.Minute
)

const (
	batchSize     = 1000 // строк на один INSERT … SELECT unnest
	maxErrorRunes = 2000
	// failTimeout — срок записи итога failed после ошибки: ctx импорта к этому моменту может быть
	// отменён, а без срока CLI и задача River повисли бы на зависшем соединении. Не уложились —
	// строку закроет как зависшую следующий запуск через StaleAfter.
	failTimeout         = 30 * time.Second
	auditAction         = "geo.import"
	codeUniqueViolation = "23505"
	runningIndex        = "geonames_imports_running"
	cityGeonameKey      = "cities_geoname_id_key"
	// admin1Unknown — код admin1 «неизвестно» в GeoNames: строки в admin1CodesASCII.txt у него нет
	admin1Unknown = "00"
	// maxListedCodes — сколько недостающих кодов admin1 перечислять в тексте ошибки
	maxListedCodes = 20

	statusSucceeded = "succeeded"
	statusFailed    = "failed"
)

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// ImportResult — итог импорта. ID заполнен и при ошибке, если строка журнала уже создана.
// PlacesSkipped — места без пригодной таймзоны (R22).
type ImportResult struct {
	ID                                                                         uuid.UUID
	PlacesUpserted, PlacesRemoved, PlacesMissing, PlacesSkipped, NamesUpserted int
	Reconciled                                                                 Reconciled
}

// Reconciled — отчёт сверки заведённых городов без geoname_id (geonames_imports.reconciled).
// Conflict — единственное место, пока шла сверка, досталось другому городу (админ активировал
// его параллельно): привязка откатилась до своего savepoint, импорт продолжился (R35).
type Reconciled struct {
	Linked    []LinkedCity    `json:"linked"`
	Ambiguous []UnmatchedCity `json:"ambiguous"`
	NotFound  []UnmatchedCity `json:"not_found"`
	Conflict  []UnmatchedCity `json:"conflict"`
}

// LinkedCity — город получил geoname_id.
type LinkedCity struct {
	CityID    uuid.UUID `json:"city_id"`
	Slug      string    `json:"slug"`
	GeonameID int64     `json:"geoname_id"`
}

// UnmatchedCity — город не тронут: кандидатов нет (NotFound), несколько (Ambiguous) или
// место заняли параллельно (Conflict).
type UnmatchedCity struct {
	CityID     uuid.UUID `json:"city_id"`
	Slug       string    `json:"slug"`
	Candidates []int64   `json:"candidates"`
}

// report — то, что лежит в geonames_imports.reconciled: сверка, счёт пропущенных таймзон и
// места с missing_since, на которые ссылается город (§5.4 шаг 4).
type report struct {
	Reconciled
	SkippedTimezone int     `json:"skipped_timezone"`
	Missing         []int64 `json:"missing"`
}

// Importer — импорт GeoNames одной страны.
type Importer struct {
	pool  *pgxpool.Pool
	clk   clock.Clock
	cfg   source.FetchConfig
	log   *slog.Logger
	fetch func(context.Context, source.FetchConfig, string) (source.Raw, error)
	// beforeLink — тестовый хук перед привязкой города (SetBeforeLink); в проде nil
	beforeLink func(ctx context.Context, cityID uuid.UUID, geonameID int64)
}

func NewImporter(pool *pgxpool.Pool, clk clock.Clock, cfg source.FetchConfig, log *slog.Logger) *Importer {
	return &Importer{pool: pool, clk: clk, cfg: cfg, log: log, fetch: source.Fetch}
}

// importRun — строка журнала этого запуска.
type importRun struct {
	id        uuid.UUID
	country   string
	locale    string // язык страны (countries.default_locale)
	startedBy *uuid.UUID
}

// Import — §5.4 шаги 1–6. startedBy — сотрудник (задача River) или nil (команда оператора);
// riverJobID — id задачи River или nil.
func (im *Importer) Import(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (ImportResult, error) {
	if !countryCode.MatchString(country) {
		return ImportResult{}, fmt.Errorf("%w: код %q — нужны две заглавные латинские буквы", ErrCountryNotFound, country)
	}
	// общий срок и для команды оператора (у задачи River свой Timeout): иначе три загрузки по
	// 15 минут переживут StaleAfter, и живой импорт закроет как зависший чужой запуск (R26)
	ctx, cancel := context.WithTimeout(ctx, ImportTimeout)
	defer cancel()
	start := time.Now()
	run, err := im.begin(ctx, country, startedBy, riverJobID)
	if err != nil {
		return ImportResult{}, err
	}
	res, files, err := im.load(ctx, run)
	if err != nil {
		// отмена ctx (остановка воркера, таймаут) не должна оставить строку running;
		// WithoutCancel снимает и срок ImportTimeout — взамен свой короткий failTimeout
		fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), failTimeout)
		ferr := im.fail(fctx, run, files, err)
		fcancel()
		if ferr != nil {
			err = errors.Join(err, fmt.Errorf("geo: журнал импорта: %w", ferr))
		}
		im.log.Error("импорт GeoNames не удался", "country", country, "import_id", run.id.String(),
			"duration", time.Since(start).String(), "err", err)
		return ImportResult{ID: run.id}, err
	}
	im.log.Info("импорт GeoNames завершён", "country", country, "import_id", run.id.String(),
		"places", res.PlacesUpserted, "removed", res.PlacesRemoved, "missing", res.PlacesMissing,
		"skipped", res.PlacesSkipped, "names", res.NamesUpserted, "linked", len(res.Reconciled.Linked),
		"ambiguous", len(res.Reconciled.Ambiguous), "not_found", len(res.Reconciled.NotFound),
		"conflict", len(res.Reconciled.Conflict), "duration", time.Since(start).String())
	return res, nil
}

// begin — шаг 1: своя строка running (или продолжение строки этой же задачи River).
func (im *Importer) begin(ctx context.Context, country string, startedBy *uuid.UUID, jobID *int64) (importRun, error) {
	run := importRun{country: country, startedBy: startedBy}
	err := db.InTx(ctx, im.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		c, err := q.ImportCountry(ctx, country)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrCountryNotFound, country)
		}
		if err != nil {
			return fmt.Errorf("geo: страна: %w", err)
		}
		run.locale = c.DefaultLocale
		now := im.clk.Now()
		if jobID != nil {
			own, err := q.ImportFindOwnRunning(ctx, geodb.ImportFindOwnRunningParams{CountryCode: country, RiverJobID: *jobID})
			switch {
			case err == nil:
				// прерванная попытка этой же задачи: шаги заново в той же строке; новое время
				// начала — иначе через StaleAfter её закрыл бы чужой запуск
				run.id = own
				return q.ImportRestart(ctx, geodb.ImportRestartParams{ID: own, StartedAt: now})
			case !errors.Is(err, pgx.ErrNoRows):
				return fmt.Errorf("geo: журнал: %w", err)
			}
		}
		stale, err := q.ImportFailStale(ctx, geodb.ImportFailStaleParams{
			CountryCode: country, Now: now, StaleBefore: now.Add(-StaleAfter),
			Reason: fmt.Sprintf("прерван: строка running старше %s — процесс остановлен без итога", StaleAfter),
		})
		if err != nil {
			return fmt.Errorf("geo: зависшие импорты: %w", err)
		}
		for _, s := range stale {
			if _, err := events.Publish(ctx, geo.ImportFinishedEvent(s, country, statusFailed)); err != nil {
				return err
			}
			// аудит закрытия — от того, кто закрыл (этот запуск), как у прочих итогов geo.import (R29)
			if err := writeAudit(ctx, importRun{id: s, country: country, startedBy: startedBy}, map[string]any{
				"country_code": country, "status": statusFailed, "error": "прерван: закрыт как зависший",
			}); err != nil {
				return err
			}
		}
		run.id = id.New()
		err = q.ImportStart(ctx, geodb.ImportStartParams{ID: run.id, CountryCode: country, StartedAt: now,
			StartedBy: startedBy, RiverJobID: jobID})
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeUniqueViolation &&
			pgErr.ConstraintName == runningIndex {
			return fmt.Errorf("%w: %s", ErrImportInProgress, country)
		}
		return err
	})
	return run, err
}

// load — шаги 2–6 для успешного пути; ошибка — строку закрывает fail. Скачанные файлы
// возвращаются и при ошибке: их sha256 и размеры fail пишет в журнал (§5.4 шаг 2, R27).
func (im *Importer) load(ctx context.Context, run importRun) (ImportResult, []source.File, error) {
	raw, err := im.fetch(ctx, im.cfg, run.country)
	if err != nil {
		return ImportResult{}, raw.Files, fmt.Errorf("geo: загрузка GeoNames: %w", err)
	}
	now := im.clk.Now()
	set := prepare(raw, run, now)
	res := ImportResult{ID: run.id, PlacesUpserted: len(set.places), PlacesSkipped: set.skippedTimezone}
	if err := checkAdmin1Refs(set); err != nil {
		return ImportResult{}, raw.Files, err
	}
	err = db.InTx(ctx, im.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		if err := checkShare(ctx, q, run.country, len(set.places)); err != nil {
			return err
		}
		if err := checkAdmin1Share(ctx, q, run.country, len(set.admin1)); err != nil {
			return err
		}
		names, err := writeSource(ctx, q, run, set)
		if err != nil {
			return err
		}
		res.NamesUpserted = names
		missing, err := q.ImportMarkMissing(ctx, geodb.ImportMarkMissingParams{CountryCode: run.country, ImportID: run.id, Now: now})
		if err != nil {
			return fmt.Errorf("geo: пропавшие места: %w", err)
		}
		removed, err := q.ImportDeleteGone(ctx, geodb.ImportDeleteGoneParams{CountryCode: run.country, ImportID: run.id})
		if err != nil {
			return fmt.Errorf("geo: удаление пропавших мест: %w", err)
		}
		slices.Sort(missing) // порядок RETURNING у UPDATE не определён — отчёт стабильный
		res.PlacesMissing, res.PlacesRemoved = len(missing), int(removed)
		if res.Reconciled, err = im.reconcile(ctx, tx, run); err != nil {
			return err
		}
		return finish(ctx, q, run, raw.Files, res, missing, now)
	})
	if err != nil {
		return ImportResult{}, raw.Files, err
	}
	if set.skippedTimezone > 0 {
		im.log.Warn("GeoNames: места без пригодной таймзоны пропущены", "country", run.country,
			"skipped", set.skippedTimezone)
	}
	return res, raw.Files, nil
}

// prepared — отобранные строки источника с выбранными названиями.
type prepared struct {
	places          []source.Place
	names           []placeName
	admin1          []source.Admin1
	admin1Names     []admin1Name
	skippedTimezone int
}

type placeName struct {
	geonameID    int64
	locale, name string
}

type admin1Name struct{ code, locale, name string }

// prepare — шаг 3: отбор мест (страна, domain.PlaceAllowed, таймзона) и названия §3.4.
func prepare(raw source.Raw, run importRun, now time.Time) prepared {
	alts := map[int64][]domain.AltName{}
	for _, a := range raw.AltNames {
		alts[a.GeonameID] = append(alts[a.GeonameID], domain.AltName{ID: a.ID, Locale: a.Locale, Name: a.Name,
			Preferred: a.Preferred, Short: a.Short, Colloquial: a.Colloquial, Historic: a.Historic, Ended: ended(a.To, now)})
	}
	var p prepared
	zones := map[string]bool{}
	seen := map[int64]bool{}
	for _, pl := range raw.Places {
		if pl.CountryCode != run.country || seen[pl.GeonameID] {
			continue
		}
		// повтор geonameid — решает первая строка, даже отвергнутая: годный дубль после негодной
		// не берётся, двойной негодный не считается в skipped дважды
		seen[pl.GeonameID] = true
		if !domain.PlaceAllowed(pl.FeatureClass, pl.FeatureCode, pl.Population) {
			continue
		}
		if !validZone(zones, pl.Timezone) {
			p.skippedTimezone++
			continue
		}
		p.places = append(p.places, pl)
		for _, l := range domain.SupportedLocales {
			if n := chooseName(alts[pl.GeonameID], l, run.locale, pl.Name, pl.ASCIIName); n != "" {
				p.names = append(p.names, placeName{pl.GeonameID, l, n})
			}
		}
	}
	codes := map[string]bool{}
	for _, a := range raw.Admin1 {
		if a.CountryCode != run.country || codes[a.Code] {
			continue
		}
		codes[a.Code] = true
		p.admin1 = append(p.admin1, a)
		for _, l := range domain.SupportedLocales {
			if n := chooseName(alts[a.GeonameID], l, run.locale, a.Name, a.ASCIIName); n != "" {
				p.admin1Names = append(p.admin1Names, admin1Name{a.Code, l, n})
			}
		}
	}
	return p
}

// chooseName — §3.4 шаг 7: запасное название для языка страны — name, для en — ascii_name,
// для прочих — нет (строка не пишется).
func chooseName(cands []domain.AltName, locale, countryLocale, name, ascii string) string {
	fallback := ""
	switch {
	case locale == countryLocale:
		fallback = name
	case locale == "en":
		fallback = ascii
	}
	return strings.TrimSpace(domain.ChooseName(cands, locale, locale == countryLocale, fallback))
}

// validZone — таймзона известна встроенной базе (clock.In: пустая и Local — отказ).
func validZone(cache map[string]bool, tz string) bool {
	ok, hit := cache[tz]
	if !hit {
		_, err := clock.In(time.Time{}, tz)
		ok = err == nil
		cache[tz] = ok
	}
	return ok
}

// ended — название вышло из употребления. В поле to GeoNames год (1946) или дата (28.07.1997,
// 1997-07-28, 19970728); название действует до конца периода. Непонятное значение — истёкшее:
// лучше не взять название, чем взять устаревшее.
func ended(to string, now time.Time) bool {
	to = strings.TrimSpace(to)
	if to == "" {
		return false
	}
	if t, err := time.Parse("2006", to); err == nil {
		return !now.Before(t.AddDate(1, 0, 0))
	}
	for _, layout := range []string{"02.01.2006", "2006-01-02", "20060102"} {
		if t, err := time.Parse(layout, to); err == nil {
			return !now.Before(t.AddDate(0, 0, 1))
		}
	}
	return true
}

// checkShare — шаг 4: новых мест не меньше 90 % прошлого успешного импорта.
func checkShare(ctx context.Context, q *geodb.Queries, country string, n int) error {
	prev, err := q.ImportLastSucceededPlaces(ctx, country)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("geo: прошлый импорт: %w", err)
	}
	if int64(n)*10 < int64(prev)*9 {
		return fmt.Errorf("%w: сейчас %d, в прошлом успешном %d — файл обрезан или подменён", ErrSourceTruncated, n, prev)
	}
	return nil
}

// checkAdmin1Share — R34 (б): строк admin1 страны в источнике не меньше 90 % от строк
// geonames_admin1 в базе (общий admin1CodesASCII.txt, обрезанный на границе строки, иначе молча
// удалил бы регионы страны). Пусто в базе — порога нет.
func checkAdmin1Share(ctx context.Context, q *geodb.Queries, country string, n int) error {
	cur, err := q.ImportAdmin1Count(ctx, country)
	if err != nil {
		return fmt.Errorf("geo: admin1 в базе: %w", err)
	}
	if int64(n)*10 < int64(cur)*9 {
		return fmt.Errorf("%w: admin1 — в источнике %d строк, в базе %d — файл admin1CodesASCII.txt обрезан или подменён",
			ErrSourceTruncated, n, cur)
	}
	return nil
}

// checkAdmin1Refs — R34 (а): каждый непустой код admin1 отобранных мест, кроме «00»
// («неизвестно» в GeoNames), есть в admin1 страны из источника. Проверка в памяти — до записи.
func checkAdmin1Refs(set prepared) error {
	known := make(map[string]bool, len(set.admin1))
	for _, a := range set.admin1 {
		known[a.Code] = true
	}
	gaps := map[string]bool{}
	for _, p := range set.places {
		if p.Admin1Code != "" && p.Admin1Code != admin1Unknown && !known[p.Admin1Code] {
			gaps[p.Admin1Code] = true
		}
	}
	if len(gaps) == 0 {
		return nil
	}
	codes := slices.Sorted(maps.Keys(gaps))
	return fmt.Errorf("%w: %s", ErrAdmin1Incomplete, codeList(codes))
}

// codeList — коды через запятую, не больше maxListedCodes; дальше — сколько ещё.
func codeList(codes []string) string {
	if len(codes) <= maxListedCodes {
		return strings.Join(codes, ", ")
	}
	return fmt.Sprintf("%s … и ещё %d", strings.Join(codes[:maxListedCodes], ", "), len(codes)-maxListedCodes)
}

// writeSource — шаг 4: места, их названия, admin1 и названия admin1 пачками по batchSize.
// Возвращает число вставленных названий.
func writeSource(ctx context.Context, q *geodb.Queries, run importRun, set prepared) (int, error) {
	for part := range slices.Chunk(set.places, batchSize) {
		arg := geodb.ImportUpsertPlacesParams{ImportID: run.id}
		for _, p := range part {
			arg.GeonameID = append(arg.GeonameID, p.GeonameID)
			arg.CountryCode = append(arg.CountryCode, p.CountryCode)
			arg.Admin1Code = append(arg.Admin1Code, p.Admin1Code)
			arg.Name = append(arg.Name, p.Name)
			arg.AsciiName = append(arg.AsciiName, p.ASCIIName)
			arg.FeatureCode = append(arg.FeatureCode, p.FeatureCode)
			arg.Population = append(arg.Population, p.Population)
			arg.Lat = append(arg.Lat, p.Lat)
			arg.Lon = append(arg.Lon, p.Lon)
			arg.Timezone = append(arg.Timezone, p.Timezone)
		}
		if err := q.ImportUpsertPlaces(ctx, arg); err != nil {
			return 0, fmt.Errorf("geo: места: %w", err)
		}
		// названия мест пересобираются: их источник — GeoNames, правок админа здесь нет
		if err := q.ImportDeletePlaceNames(ctx, arg.GeonameID); err != nil {
			return 0, fmt.Errorf("geo: названия мест: %w", err)
		}
	}
	names := 0
	for part := range slices.Chunk(set.names, batchSize) {
		var arg geodb.ImportInsertPlaceNamesParams
		for _, n := range part {
			arg.GeonameID = append(arg.GeonameID, n.geonameID)
			arg.Locale = append(arg.Locale, n.locale)
			arg.Name = append(arg.Name, n.name)
		}
		n, err := q.ImportInsertPlaceNames(ctx, arg)
		if err != nil {
			return 0, fmt.Errorf("geo: названия мест: %w", err)
		}
		names += int(n)
	}
	if err := q.ImportDeleteAdmin1Names(ctx, run.country); err != nil {
		return 0, fmt.Errorf("geo: названия регионов: %w", err)
	}
	keep := make([]string, 0, len(set.admin1))
	for part := range slices.Chunk(set.admin1, batchSize) {
		arg := geodb.ImportUpsertAdmin1Params{CountryCode: run.country}
		for _, a := range part {
			arg.Admin1Code = append(arg.Admin1Code, a.Code)
			arg.GeonameID = append(arg.GeonameID, a.GeonameID)
			arg.AsciiName = append(arg.AsciiName, a.ASCIIName)
		}
		if err := q.ImportUpsertAdmin1(ctx, arg); err != nil {
			return 0, fmt.Errorf("geo: регионы: %w", err)
		}
		keep = append(keep, arg.Admin1Code...)
	}
	if err := q.ImportDeleteStaleAdmin1(ctx, geodb.ImportDeleteStaleAdmin1Params{CountryCode: run.country, Keep: keep}); err != nil {
		return 0, fmt.Errorf("geo: пропавшие регионы: %w", err)
	}
	for part := range slices.Chunk(set.admin1Names, batchSize) {
		arg := geodb.ImportInsertAdmin1NamesParams{CountryCode: run.country}
		for _, n := range part {
			arg.Admin1Code = append(arg.Admin1Code, n.code)
			arg.Locale = append(arg.Locale, n.locale)
			arg.Name = append(arg.Name, n.name)
		}
		n, err := q.ImportInsertAdmin1Names(ctx, arg)
		if err != nil {
			return 0, fmt.Errorf("geo: названия регионов: %w", err)
		}
		names += int(n)
	}
	return names, nil
}

// reconcile — шаг 5: сверка заведённых городов страны без geoname_id.
func (im *Importer) reconcile(ctx context.Context, tx pgx.Tx, run importRun) (Reconciled, error) {
	q := geodb.New(tx)
	rec := Reconciled{Linked: []LinkedCity{}, Ambiguous: []UnmatchedCity{}, NotFound: []UnmatchedCity{}, Conflict: []UnmatchedCity{}}
	cities, err := q.ImportUnlinkedCities(ctx, run.country)
	if err != nil {
		return rec, fmt.Errorf("geo: сверка: %w", err)
	}
	type match struct {
		city  geodb.ImportUnlinkedCitiesRow
		cands []geodb.ImportCityCandidatesRow
	}
	matches := make([]match, 0, len(cities))
	claims := map[int64]int{} // место → сколько городов на него претендует
	for _, c := range cities {
		cands, err := q.ImportCityCandidates(ctx, geodb.ImportCityCandidatesParams{CityID: c.ID, CountryCode: run.country})
		if err != nil {
			return rec, fmt.Errorf("geo: сверка %s: %w", c.Slug, err)
		}
		matches = append(matches, match{c, cands})
		for _, p := range cands {
			claims[p.GeonameID]++
		}
	}
	for _, m := range matches {
		ids := make([]int64, 0, len(m.cands))
		for _, p := range m.cands {
			ids = append(ids, p.GeonameID)
		}
		switch {
		case len(m.cands) == 0:
			rec.NotFound = append(rec.NotFound, UnmatchedCity{CityID: m.city.ID, Slug: m.city.Slug, Candidates: ids})
		case len(m.cands) > 1 || claims[m.cands[0].GeonameID] > 1:
			// несколько мест или одно место на несколько городов — решает админ
			rec.Ambiguous = append(rec.Ambiguous, UnmatchedCity{CityID: m.city.ID, Slug: m.city.Slug, Candidates: ids})
		default:
			if im.beforeLink != nil {
				im.beforeLink(ctx, m.city.ID, m.cands[0].GeonameID)
			}
			taken, err := linkSavepoint(ctx, tx, run, m.city, m.cands[0])
			switch {
			case err != nil:
				return rec, err
			case taken:
				rec.Conflict = append(rec.Conflict, UnmatchedCity{CityID: m.city.ID, Slug: m.city.Slug, Candidates: ids})
			default:
				rec.Linked = append(rec.Linked, LinkedCity{CityID: m.city.ID, Slug: m.city.Slug, GeonameID: m.cands[0].GeonameID})
			}
		}
	}
	return rec, nil
}

// linkSavepoint — привязка во вложенной транзакции (SAVEPOINT). Гонка с админом — откатывается
// только эта привязка, taken = true, импорт продолжается (R35): место успели отдать другому
// городу (23505 cities_geoname_id_key) или сам город успели привязать (ImportLinkCity без строки:
// geoname_id уже не NULL, единственный источник ErrNoRows в link). Прочие ошибки — ошибка импорта.
// События и аудит пишутся в tx из ctx — на том же соединении, поэтому откат до savepoint
// снимает и событие geo.city_linked.
func linkSavepoint(ctx context.Context, tx pgx.Tx, run importRun, c geodb.ImportUnlinkedCitiesRow,
	p geodb.ImportCityCandidatesRow) (taken bool, err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("geo: savepoint привязки %s: %w", c.Slug, err)
	}
	if err := link(ctx, geodb.New(sp), run, c, p); err != nil {
		if rerr := sp.Rollback(ctx); rerr != nil {
			return false, errors.Join(err, fmt.Errorf("geo: откат привязки %s: %w", c.Slug, rerr))
		}
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeUniqueViolation &&
			pgErr.ConstraintName == cityGeonameKey {
			return true, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	if err := sp.Commit(ctx); err != nil {
		return false, fmt.Errorf("geo: savepoint привязки %s: %w", c.Slug, err)
	}
	return false, nil
}

// link — город получает geoname_id, version + 1, недостающие переводы; регион — код admin1 и
// недостающие переводы; событие geo.city_linked. centroid и population города не меняются.
func link(ctx context.Context, q *geodb.Queries, run importRun, c geodb.ImportUnlinkedCitiesRow, p geodb.ImportCityCandidatesRow) error {
	version, err := q.ImportLinkCity(ctx, geodb.ImportLinkCityParams{ID: c.ID, GeonameID: p.GeonameID})
	if err != nil {
		return fmt.Errorf("geo: привязка %s: %w", c.Slug, err)
	}
	if _, err := q.ImportCityNamesFromPlace(ctx, geodb.ImportCityNamesFromPlaceParams{
		CityID: c.ID, GeonameID: p.GeonameID, CountryLocale: run.locale}); err != nil {
		return fmt.Errorf("geo: переводы %s: %w", c.Slug, err)
	}
	if c.RegionID != nil && p.Admin1Code != nil {
		if _, err := q.ImportLinkRegion(ctx, geodb.ImportLinkRegionParams{ID: *c.RegionID, Admin1Code: *p.Admin1Code}); err != nil {
			return fmt.Errorf("geo: код региона %s: %w", c.Slug, err)
		}
		if _, err := q.ImportRegionNamesFromAdmin1(ctx, geodb.ImportRegionNamesFromAdmin1Params{
			RegionID: *c.RegionID, CountryCode: run.country, Admin1Code: *p.Admin1Code, CountryLocale: run.locale}); err != nil {
			return fmt.Errorf("geo: переводы региона %s: %w", c.Slug, err)
		}
	}
	_, err = events.Publish(ctx, geo.CityLinkedEvent(c.ID, version, p.GeonameID))
	return err
}

type journalFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// journalFiles — source_files журнала; nil — файлов нет (ошибка до первого скачанного).
func journalFiles(files []source.File) ([]byte, error) {
	if len(files) == 0 {
		return nil, nil
	}
	jf := make([]journalFile, 0, len(files))
	for _, f := range files {
		jf = append(jf, journalFile{Name: f.Name, URL: f.URL, Bytes: f.Bytes, SHA256: f.SHA256})
	}
	return json.Marshal(jf)
}

// finish — шаг 6, успех: итог в журнал, событие, аудит — в транзакции записи. missing —
// geoname_id мест с missing_since, на которые ссылается город (§5.4 шаг 4 — «в отчёт»).
func finish(ctx context.Context, q *geodb.Queries, run importRun, files []source.File, res ImportResult, missing []int64, now time.Time) error {
	filesJSON, err := journalFiles(files)
	if err != nil {
		return err
	}
	if filesJSON == nil {
		filesJSON = []byte("[]")
	}
	if missing == nil {
		missing = []int64{}
	}
	rep, err := json.Marshal(report{Reconciled: res.Reconciled, SkippedTimezone: res.PlacesSkipped, Missing: missing})
	if err != nil {
		return err
	}
	n, err := q.ImportSucceed(ctx, geodb.ImportSucceedParams{
		ID: run.id, Now: now, SourceFiles: filesJSON, Reconciled: rep,
		PlacesUpserted: int64(res.PlacesUpserted), PlacesRemoved: int64(res.PlacesRemoved),
		PlacesMissing: int64(res.PlacesMissing), NamesUpserted: int64(res.NamesUpserted),
	})
	if err != nil {
		return fmt.Errorf("geo: журнал: %w", err)
	}
	if n == 0 {
		return ErrImportClosed
	}
	if _, err := events.Publish(ctx, geo.ImportFinishedEvent(run.id, run.country, statusSucceeded)); err != nil {
		return err
	}
	return writeAudit(ctx, run, map[string]any{
		"country_code": run.country, "status": statusSucceeded,
		"places_upserted": res.PlacesUpserted, "places_removed": res.PlacesRemoved,
		"places_missing": res.PlacesMissing, "names_upserted": res.NamesUpserted,
		"cities_linked": len(res.Reconciled.Linked), "cities_ambiguous": len(res.Reconciled.Ambiguous),
		"cities_not_found": len(res.Reconciled.NotFound), "cities_conflict": len(res.Reconciled.Conflict),
		"skipped_timezone": res.PlacesSkipped,
	})
}

// fail — шаг 6, ошибка: строка → failed с текстом и отпечатками уже скачанных файлов (R27),
// событие, аудит. Строку уже закрыл другой запуск — итог записан там, здесь ничего.
func (im *Importer) fail(ctx context.Context, run importRun, files []source.File, cause error) error {
	reason := []rune(cause.Error())
	if len(reason) > maxErrorRunes {
		reason = reason[:maxErrorRunes]
	}
	filesJSON, err := journalFiles(files)
	if err != nil {
		return err
	}
	return db.InTx(ctx, im.pool, func(ctx context.Context, tx pgx.Tx) error {
		n, err := geodb.New(tx).ImportFail(ctx, geodb.ImportFailParams{ID: run.id, Now: im.clk.Now(),
			Reason: string(reason), SourceFiles: filesJSON})
		if err != nil || n == 0 {
			return err
		}
		if _, err := events.Publish(ctx, geo.ImportFinishedEvent(run.id, run.country, statusFailed)); err != nil {
			return err
		}
		return writeAudit(ctx, run, map[string]any{"country_code": run.country, "status": statusFailed, "error": string(reason)})
	})
}

// writeAudit — команда оператора: роль operator без автора; задача River: сотрудник, роль admin
// (импорт ставит только admin, §5.2).
func writeAudit(ctx context.Context, run importRun, after map[string]any) error {
	actor, role := uuid.Nil, "operator"
	if run.startedBy != nil {
		actor, role = *run.startedBy, "admin"
	}
	return audit.Write(ctx, audit.Entry{ActorUserID: actor, ActorRole: role, Action: auditAction,
		ObjectType: geo.AggregateGeonamesImport, ObjectID: run.id, After: after})
}
