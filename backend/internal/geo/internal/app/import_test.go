package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/geo/internal/source"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

// env — база под ролью worker (права grants.sql, как у команды оператора) и часы теста.
type env struct {
	owner *pgxpool.Pool // подготовка данных и проверки
	as    *pgxpool.Pool // импорт — под ролью worker
	clk   *clocktest.Fake
}

func newEnv(t *testing.T) env {
	t.Helper()
	p := dbtest.NewPoolsAs(t, "worker")
	return env{owner: p.Owner, as: p.As, clk: clocktest.New(t0)}
}

// importer — импорт с подменённой загрузкой: raw читается при каждом вызове Import.
func (e env) importer(raw *source.Raw) *app.Importer {
	im := app.NewImporter(e.as, e.clk, source.FetchConfig{}, slog.New(slog.DiscardHandler))
	im.SetFetch(func(context.Context, source.FetchConfig, string) (source.Raw, error) { return *raw, nil })
	return im
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "source", "testdata", name)) //nolint:gosec // выдержки GeoNames пакета source
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixture — выдержки GeoNames пакета source целиком, без фильтра Fetch: отбор мест — забота
// импорта (Зюзино PPLX, деревня без населения 802557, Орловка PPLQ без таймзоны отсеиваются).
func fixture(t *testing.T) source.Raw {
	t.Helper()
	places, err := source.ParsePlaces(bytes.NewReader(readTestdata(t, "RU.txt")))
	if err != nil {
		t.Fatal(err)
	}
	alts, err := source.ParseAltNames(bytes.NewReader(readTestdata(t, "alternateNamesV2-RU.txt")))
	if err != nil {
		t.Fatal(err)
	}
	admin1, err := source.ParseAdmin1(bytes.NewReader(readTestdata(t, "admin1CodesASCII.txt")), "RU")
	if err != nil {
		t.Fatal(err)
	}
	return source.Raw{Places: places, AltNames: alts, Admin1: admin1, Files: []source.File{{
		Name: "RU.zip", URL: "https://download.geonames.org/export/dump/RU.zip", Bytes: 42, SHA256: strings.Repeat("ab", 32),
	}}}
}

// synth — n мест-заглушек региона 70 (класс P, население 1000): объём для порога 90 %.
func synth(from int64, n int) []source.Place {
	out := make([]source.Place, n)
	for i := range out {
		gid := from + int64(i)
		out[i] = source.Place{GeonameID: gid, Name: fmt.Sprintf("Synth %d", gid), ASCIIName: fmt.Sprintf("Synth %d", gid),
			FeatureClass: "P", FeatureCode: "PPL", CountryCode: "RU", Admin1Code: "70", Timezone: "Europe/Moscow",
			Population: 1000, Lat: 45, Lon: 42}
	}
	return out
}

// named — место с предпочтительным ru-названием (для сверки).
func named(raw *source.Raw, gid int64, ascii, ru, admin1 string) {
	raw.Places = append(raw.Places, source.Place{GeonameID: gid, Name: ascii, ASCIIName: ascii, FeatureClass: "P",
		FeatureCode: "PPL", CountryCode: "RU", Admin1Code: admin1, Timezone: "Europe/Moscow", Population: 5000, Lat: 50, Lon: 50})
	raw.AltNames = append(raw.AltNames, source.AltName{ID: gid * 10, GeonameID: gid, Locale: "ru", Name: ru, Preferred: true})
}

func without(raw source.Raw, ids ...int64) source.Raw {
	out := raw
	out.Places = slices.DeleteFunc(slices.Clone(raw.Places), func(p source.Place) bool { return slices.Contains(ids, p.GeonameID) })
	return out
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

type journal struct {
	Status     string
	Error      *string
	StartedBy  *uuid.UUID
	RiverJobID *int64
	Places     *int32
	Files      []byte
	Reconciled []byte
}

func journalRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) journal {
	t.Helper()
	var j journal
	if err := pool.QueryRow(context.Background(), `SELECT status, error, started_by, river_job_id, places_upserted,
		source_files, reconciled FROM geonames_imports WHERE id = $1`, id).
		Scan(&j.Status, &j.Error, &j.StartedBy, &j.RiverJobID, &j.Places, &j.Files, &j.Reconciled); err != nil {
		t.Fatalf("журнал %s: %v", id, err)
	}
	return j
}

func finishedStatus(t *testing.T, pool *pgxpool.Pool, importID uuid.UUID) string {
	t.Helper()
	var payload []byte
	if err := pool.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE event_type = $1 AND aggregate_id = $2`,
		geo.EventImportFinished, importID).Scan(&payload); err != nil {
		t.Fatalf("geo.import_finished для %s: %v", importID, err)
	}
	var f geo.ImportFinished
	if err := json.Unmarshal(payload, &f); err != nil || f.ImportID != importID || f.CountryCode != "RU" {
		t.Fatalf("payload %s: %v", payload, err)
	}
	return f.Status
}

func pairs(t *testing.T, pool *pgxpool.Pool, sql string) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

func TestImportLoadsPlacesNamesAndJournal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	raw := fixture(t)
	res, err := e.importer(&raw).Import(ctx, "RU", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 3 места × ru/en + 3 региона × ru/en
	if res.PlacesUpserted != 3 || res.PlacesRemoved != 0 || res.PlacesMissing != 0 || res.NamesUpserted != 12 {
		t.Fatalf("итог: %+v", res)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places"); n != 3 {
		t.Fatalf("мест в базе %d", n)
	}
	var lat, lon float64
	var tz, admin1 string
	var importID uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT ST_Y(location::geometry), ST_X(location::geometry), timezone, admin1_code, import_id
		FROM geonames_places WHERE geoname_id = 487846`).Scan(&lat, &lon, &tz, &admin1, &importID); err != nil {
		t.Fatal(err)
	}
	if lat != 45.03442 || lon != 41.9642 || tz != "Europe/Moscow" || admin1 != "70" || importID != res.ID {
		t.Fatalf("Ставрополь: %v %v %s %s %s", lat, lon, tz, admin1, importID)
	}
	// названия §3.4 (проверено по данным 02.10.2026): ru Ставрополя — шаг 3, en — шаг 4
	places := pairs(t, e.owner, "SELECT geoname_id::text || '/' || locale, name FROM geonames_place_names")
	for k, v := range map[string]string{
		"487846/ru": "Ставрополь", "487846/en": "Stavropol", "493702/ru": "Михайловск", "493702/en": "Mikhaylovsk",
		"526815/ru": "Михайловск", "526815/en": "Mikhaylovsk",
	} {
		if places[k] != v {
			t.Errorf("место %s = %q, ждали %q", k, places[k], v)
		}
	}
	// регионы: ru — полное «Ставропольский Край», не краткое предпочтительное; нет переводов —
	// name файла (язык страны) и ascii (en)
	regions := pairs(t, e.owner, "SELECT admin1_code || '/' || locale, name FROM geonames_admin1_names")
	for k, v := range map[string]string{
		"70/ru": "Ставропольский Край", "70/en": "Stavropol Kray", "71/ru": "Свердловская Область",
		"71/en": "Sverdlovsk Oblast", "48/ru": "Moscow", "48/en": "Moscow",
	} {
		if regions[k] != v {
			t.Errorf("регион %s = %q, ждали %q", k, regions[k], v)
		}
	}
	j := journalRow(t, e.owner, res.ID)
	if j.Status != "succeeded" || j.StartedBy != nil || j.RiverJobID != nil || j.Places == nil || *j.Places != 3 {
		t.Fatalf("журнал: %+v", j)
	}
	var files []struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Bytes  int64  `json:"bytes"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(j.Files, &files); err != nil || len(files) != 1 || files[0].Name != "RU.zip" ||
		files[0].Bytes != 42 || files[0].SHA256 != strings.Repeat("ab", 32) || files[0].URL == "" {
		t.Fatalf("source_files: %v %s", err, j.Files)
	}
	if s := finishedStatus(t, e.owner, res.ID); s != "succeeded" {
		t.Fatalf("событие: %s", s)
	}
	var role string
	var actor *uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT actor_role, actor_user_id FROM audit_log WHERE action = 'geo.import' AND object_id = $1`,
		res.ID).Scan(&role, &actor); err != nil || role != "operator" || actor != nil {
		t.Fatalf("аудит: %v %q %v", err, role, actor)
	}
}

func TestImportByStaffRecordsActor(t *testing.T) {
	e := newEnv(t)
	raw := fixture(t)
	staff := uuid.New()
	res, err := e.importer(&raw).Import(context.Background(), "RU", &staff, ptr(int64(42)))
	if err != nil {
		t.Fatal(err)
	}
	j := journalRow(t, e.owner, res.ID)
	if j.StartedBy == nil || *j.StartedBy != staff || j.RiverJobID == nil || *j.RiverJobID != 42 {
		t.Fatalf("журнал: %+v", j)
	}
	var role string
	var actor *uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT actor_role, actor_user_id FROM audit_log WHERE object_id = $1`,
		res.ID).Scan(&role, &actor); err != nil || role != "admin" || actor == nil || *actor != staff {
		t.Fatalf("аудит: %v %q %v", err, role, actor)
	}
}

// Review Focus 4: второй запуск той же страны ничего не меняет.
func TestImportTwiceIsIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	raw := fixture(t)
	im := e.importer(&raw)
	snapshot := func() string {
		var places, names string
		if err := e.owner.QueryRow(ctx, `SELECT
			(SELECT string_agg(format('%s|%s|%s|%s|%s|%s', geoname_id, name, population, ST_AsText(location::geometry),
				timezone, missing_since IS NULL), ';' ORDER BY geoname_id) FROM geonames_places),
			(SELECT string_agg(format('%s|%s|%s', geoname_id, locale, name), ';' ORDER BY geoname_id, locale)
				FROM geonames_place_names)`).Scan(&places, &names); err != nil {
			t.Fatal(err)
		}
		return places + "\n" + names
	}
	if _, err := im.Import(ctx, "RU", nil, nil); err != nil {
		t.Fatal(err)
	}
	before := snapshot()
	e.clk.Advance(time.Hour)
	res, err := im.Import(ctx, "RU", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlacesUpserted != 3 || res.PlacesRemoved != 0 || res.PlacesMissing != 0 || res.NamesUpserted != 12 {
		t.Fatalf("повтор: %+v", res)
	}
	if after := snapshot(); after != before {
		t.Fatalf("повтор изменил данные:\n%s\n---\n%s", before, after)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_imports WHERE status = 'succeeded'"); n != 2 {
		t.Fatalf("успешных строк журнала %d", n)
	}
}

// Пропавшие места удаляются; место, на которое ссылается город (Ставрополь сида), остаётся с
// missing_since; вернулось в источник — отметка снимается. 27 из 30 — ровно порог 90 %.
func TestImportRemovesGoneAndKeepsReferenced(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := fixture(t)
	base.Places = append(base.Places, synth(9000001, 27)...)
	raw := base
	im := e.importer(&raw)
	if res, err := im.Import(ctx, "RU", nil, nil); err != nil || res.PlacesUpserted != 30 {
		t.Fatalf("первый: %v %+v", err, res)
	}
	// пропали оба места городов сида (Ставрополь, Михайловск) и одно место без города
	raw = without(base, 487846, 493702, 9000001)
	e.clk.Advance(time.Hour)
	res, err := im.Import(ctx, "RU", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlacesUpserted != 27 || res.PlacesRemoved != 1 || res.PlacesMissing != 2 {
		t.Fatalf("второй: %+v", res)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places WHERE geoname_id = 9000001"); n != 0 {
		t.Fatal("пропавшее место без города не удалено")
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_place_names WHERE geoname_id = 9000001"); n != 0 {
		t.Fatal("названия удалённого места остались")
	}
	var missing *time.Time
	if err := e.owner.QueryRow(ctx, "SELECT missing_since FROM geonames_places WHERE geoname_id = 487846").Scan(&missing); err != nil {
		t.Fatalf("место города удалено: %v", err)
	}
	if missing == nil || !missing.Equal(t0.Add(time.Hour)) {
		t.Fatalf("missing_since = %v", missing)
	}
	// §5.4 шаг 4: места городов с missing_since — в отчёте журнала, по возрастанию (R28). Нынешний
	// план ImportMarkMissing и сам идёт по geoname_id (индексы), slices.Sort в import.go страхует
	// от смены плана — подсадкой данными его не уронить (раунд 1, отчёт Task 10)
	var rep struct {
		Missing []int64 `json:"missing"`
	}
	if err := json.Unmarshal(journalRow(t, e.owner, res.ID).Reconciled, &rep); err != nil || !slices.Equal(rep.Missing, []int64{487846, 493702}) {
		t.Fatalf("отчёт missing: %v %+v", err, rep)
	}
	raw = base
	if res, err := im.Import(ctx, "RU", nil, nil); err != nil || res.PlacesMissing != 0 {
		t.Fatalf("третий: %v %+v", err, res)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places WHERE missing_since IS NOT NULL"); n != 0 {
		t.Fatal("вернувшееся место осталось с missing_since")
	}
}

// Review Focus 3: обрезанный файл (меньше 90 % прошлого импорта) — ошибка в журнале, ничего не
// удалено; ровно 90 % — проходит.
func TestImportRejectsTruncatedSource(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := fixture(t)
	base.Places = append(base.Places, synth(9000001, 7)...) // 3 + 7 = 10 мест
	raw := base
	im := e.importer(&raw)
	if _, err := im.Import(ctx, "RU", nil, nil); err != nil {
		t.Fatal(err)
	}
	raw = without(base, 9000001, 9000002) // 8 из 10
	res, err := im.Import(ctx, "RU", nil, nil)
	if !errors.Is(err, app.ErrSourceTruncated) {
		t.Fatalf("err = %v", err)
	}
	j := journalRow(t, e.owner, res.ID)
	if j.Status != "failed" || j.Error == nil || !strings.Contains(*j.Error, "90 %") {
		t.Fatalf("журнал: %+v", j)
	}
	// R27: отпечаток отвергнутого файла — в журнале и при ошибке
	if !strings.Contains(string(j.Files), strings.Repeat("ab", 32)) {
		t.Fatalf("source_files при ошибке: %s", j.Files)
	}
	if s := finishedStatus(t, e.owner, res.ID); s != "failed" {
		t.Fatalf("событие: %s", s)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places"); n != 10 {
		t.Fatalf("после отказа мест %d — что-то удалено", n)
	}
	raw = without(base, 9000001) // 9 из 10 — ровно порог
	if res, err := im.Import(ctx, "RU", nil, nil); err != nil || res.PlacesRemoved != 1 {
		t.Fatalf("9 из 10: %v %+v", err, res)
	}
}

type entry struct{ name, body string }

func zipOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, en := range entries {
		w, err := zw.Create(en.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(en.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Review Focus 3 сквозь настоящий source.Fetch: лишняя запись в zip, редирект на чужой хост,
// файл больше лимита — импорт падает с понятной ошибкой в журнале и ничего не удаляет.
func TestImportFetchFailuresKeepData(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ru := string(readTestdata(t, "RU.txt"))
	files := map[string][]byte{
		"/export/dump/admin1CodesASCII.txt":  readTestdata(t, "admin1CodesASCII.txt"),
		"/export/dump/RU.zip":                zipOf(t, entry{"readme.txt", "readme"}, entry{"RU.txt", ru}),
		"/export/dump/alternatenames/RU.zip": zipOf(t, entry{"RU.txt", string(readTestdata(t, "alternateNamesV2-RU.txt"))}),
	}
	serveFiles := func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}
	foreign := httptest.NewTLSServer(http.HandlerFunc(serveFiles)) // отдал бы настоящий файл
	defer foreign.Close()
	extra := zipOf(t, entry{"RU.txt", ru}, entry{"evil.sh", "echo pwned"})
	var mode atomic.Value
	mode.Store("")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/export/dump/RU.zip" {
			switch mode.Load() {
			case "extra":
				_, _ = w.Write(extra)
				return
			case "redirect":
				http.Redirect(w, r, foreign.URL+r.URL.Path, http.StatusFound)
				return
			}
		}
		serveFiles(w, r)
	}))
	defer srv.Close()
	cfg := source.FetchConfig{BaseURL: srv.URL, MaxCompressed: 1 << 20, MaxUncompressed: 1 << 20, HTTPClient: srv.Client()}
	discard := slog.New(slog.DiscardHandler)
	if res, err := app.NewImporter(e.as, e.clk, cfg, discard).Import(ctx, "RU", nil, nil); err != nil || res.PlacesUpserted != 3 {
		t.Fatalf("исходный импорт: %v %+v", err, res)
	}
	small := cfg
	small.MaxCompressed = 64
	for _, tc := range []struct {
		name, mode, text string
		cfg              source.FetchConfig
		is               error
	}{
		{"лишняя запись в zip", "extra", "лишняя запись", cfg, source.ErrBadArchive},
		{"редирект на чужой хост", "redirect", "другой хост", cfg, source.ErrForeignRedirect},
		{"файл больше лимита", "", "больше лимита", small, source.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode.Store(tc.mode)
			res, err := app.NewImporter(e.as, e.clk, tc.cfg, discard).Import(ctx, "RU", nil, nil)
			if !errors.Is(err, tc.is) {
				t.Fatalf("err = %v", err)
			}
			j := journalRow(t, e.owner, res.ID)
			if j.Status != "failed" || j.Error == nil || !strings.Contains(*j.Error, tc.text) {
				t.Fatalf("журнал: %+v", j)
			}
			// R27: отпечаток подменённого архива — в журнале
			if tc.mode == "extra" {
				sum := sha256.Sum256(extra)
				if !strings.Contains(string(j.Files), hex.EncodeToString(sum[:])) {
					t.Fatalf("source_files без sha256 подменённого архива: %s", j.Files)
				}
			}
			if n := count(t, e.owner, "SELECT count(*) FROM geonames_places WHERE missing_since IS NULL"); n != 3 {
				t.Fatalf("мест %d — данные тронуты", n)
			}
		})
	}
}

// Review Focus 4: строка running от убитого процесса не блокирует навсегда; живой импорт — 409;
// повтор той же задачи River продолжает свою строку.
func TestImportStaleAndInterruptedRuns(t *testing.T) {
	insertRunning := func(t *testing.T, e env, startedAt time.Time, job *int64) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO geonames_imports (id, country_code, status, started_at, river_job_id)
			VALUES ($1, 'RU', 'running', $2, $3)`, id, startedAt, job); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ctx := context.Background()
	for _, age := range []time.Duration{5 * time.Minute, 39 * time.Minute} {
		t.Run(fmt.Sprintf("живой импорт %s — занято", age), func(t *testing.T) {
			e := newEnv(t)
			raw := fixture(t)
			other := insertRunning(t, e, t0.Add(-age), ptr(int64(55)))
			if _, err := e.importer(&raw).Import(ctx, "RU", nil, ptr(int64(77))); !errors.Is(err, app.ErrImportInProgress) {
				t.Fatalf("err = %v", err)
			}
			if j := journalRow(t, e.owner, other); j.Status != "running" {
				t.Fatalf("чужая строка: %+v", j)
			}
			if n := count(t, e.owner, "SELECT count(*) FROM geonames_imports"); n != 1 {
				t.Fatalf("строк журнала %d", n)
			}
		})
	}
	t.Run("зависшая дольше 40 минут закрывается", func(t *testing.T) {
		e := newEnv(t)
		raw := fixture(t)
		stale := insertRunning(t, e, t0.Add(-41*time.Minute), nil)
		staff := uuid.New()
		res, err := e.importer(&raw).Import(ctx, "RU", &staff, ptr(int64(77)))
		if err != nil || res.ID == stale {
			t.Fatalf("%v %+v", err, res)
		}
		j := journalRow(t, e.owner, stale)
		if j.Status != "failed" || j.Error == nil || !strings.Contains(*j.Error, "прерван") {
			t.Fatalf("зависшая строка: %+v", j)
		}
		if s := finishedStatus(t, e.owner, stale); s != "failed" {
			t.Fatalf("событие зависшей: %s", s)
		}
		// R29: закрытие зависшей — в аудите, как прочие итоги geo.import, от закрывшего запуска
		if n := count(t, e.owner, "SELECT count(*) FROM audit_log WHERE action = 'geo.import' AND object_id = $1", stale); n != 1 {
			t.Fatalf("аудит зависшей: %d", n)
		}
		var role string
		var actor *uuid.UUID
		if err := e.owner.QueryRow(ctx, `SELECT actor_role, actor_user_id FROM audit_log WHERE action = 'geo.import' AND object_id = $1`,
			stale).Scan(&role, &actor); err != nil || role != "admin" || actor == nil || *actor != staff {
			t.Fatalf("автор аудита зависшей: %v %q %v", err, role, actor)
		}
	})
	t.Run("повтор той же задачи продолжает свою строку", func(t *testing.T) {
		e := newEnv(t)
		raw := fixture(t)
		own := insertRunning(t, e, t0.Add(-time.Minute), ptr(int64(77)))
		res, err := e.importer(&raw).Import(ctx, "RU", nil, ptr(int64(77)))
		if err != nil || res.ID != own {
			t.Fatalf("%v: id %s, ждали свою строку %s", err, res.ID, own)
		}
		if j := journalRow(t, e.owner, own); j.Status != "succeeded" {
			t.Fatalf("своя строка: %+v", j)
		}
		if n := count(t, e.owner, "SELECT count(*) FROM geonames_imports"); n != 1 {
			t.Fatalf("строк журнала %d — создана лишняя", n)
		}
	})
}

// Отмена ctx (SIGTERM воркера, таймаут River) — строка закрывается failed на WithoutCancel.
func TestImportCancelledMarksJournalFailed(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	im := app.NewImporter(e.as, e.clk, source.FetchConfig{}, slog.New(slog.DiscardHandler))
	im.SetFetch(func(ctx context.Context, _ source.FetchConfig, _ string) (source.Raw, error) {
		cancel()
		<-ctx.Done()
		return source.Raw{}, ctx.Err()
	})
	res, err := im.Import(ctx, "RU", nil, nil)
	if !errors.Is(err, context.Canceled) || res.ID == uuid.Nil {
		t.Fatalf("%v %+v", err, res)
	}
	j := journalRow(t, e.owner, res.ID)
	if j.Status != "failed" || j.Error == nil || !strings.Contains(*j.Error, "context canceled") {
		t.Fatalf("журнал: %+v", j)
	}
	if s := finishedStatus(t, e.owner, res.ID); s != "failed" {
		t.Fatalf("событие: %s", s)
	}
}

// R26: у Import свой срок ImportTimeout — и у команды оператора, где ctx без срока.
func TestImportHasOwnDeadline(t *testing.T) {
	e := newEnv(t)
	raw := fixture(t)
	im := app.NewImporter(e.as, e.clk, source.FetchConfig{}, slog.New(slog.DiscardHandler))
	var deadline time.Time
	var ok bool
	im.SetFetch(func(ctx context.Context, _ source.FetchConfig, _ string) (source.Raw, error) {
		deadline, ok = ctx.Deadline()
		return raw, nil
	})
	before := time.Now()
	if _, err := im.Import(context.Background(), "RU", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !ok || deadline.After(before.Add(app.ImportTimeout).Add(time.Second)) || deadline.Before(before.Add(app.ImportTimeout-time.Minute)) {
		t.Fatalf("срок загрузки: %v (есть: %v), ждали ≈ now+%s", deadline, ok, app.ImportTimeout)
	}
}

func TestImportUnknownCountry(t *testing.T) {
	e := newEnv(t)
	raw := fixture(t)
	for _, cc := range []string{"ZZ", "ru", "RUS"} {
		if _, err := e.importer(&raw).Import(context.Background(), cc, nil, nil); !errors.Is(err, app.ErrCountryNotFound) {
			t.Errorf("%q: %v", cc, err)
		}
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_imports"); n != 0 {
		t.Fatalf("строк журнала %d", n)
	}
}

// Пустая, Local и неизвестная таймзона — место пропускается, счёт — в отчёте.
func TestImportSkipsUnusableTimezones(t *testing.T) {
	e := newEnv(t)
	raw := fixture(t)
	bad := synth(9200001, 3)
	bad[0].Timezone, bad[1].Timezone, bad[2].Timezone = "", "Local", "Mars/Olympus"
	raw.Places = append(raw.Places, bad...)
	res, err := e.importer(&raw).Import(context.Background(), "RU", nil, nil)
	if err != nil || res.PlacesUpserted != 3 || res.PlacesSkipped != 3 {
		t.Fatalf("%v %+v", err, res)
	}
	var rep struct {
		SkippedTimezone int `json:"skipped_timezone"`
	}
	if err := json.Unmarshal(journalRow(t, e.owner, res.ID).Reconciled, &rep); err != nil || rep.SkippedTimezone != 3 {
		t.Fatalf("отчёт: %v %+v", err, rep)
	}
}

// Повтор geonameid — решает первая строка, даже негодная: годный дубль после негодной не берётся,
// двойной негодный дубль считается в skipped_timezone один раз.
func TestImportDuplicatePlaceFirstRowWins(t *testing.T) {
	e := newEnv(t)
	raw := fixture(t)
	badThenGood := synth(9400001, 1)
	badThenGood = append(badThenGood, badThenGood[0])
	badThenGood[0].Timezone = ""
	badTwice := synth(9400002, 1)
	badTwice = append(badTwice, badTwice[0])
	badTwice[0].Timezone, badTwice[1].Timezone = "Mars/Olympus", "Mars/Olympus"
	raw.Places = append(append(raw.Places, badThenGood...), badTwice...)
	res, err := e.importer(&raw).Import(context.Background(), "RU", nil, nil)
	if err != nil || res.PlacesUpserted != 3 || res.PlacesSkipped != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places WHERE geoname_id IN (9400001, 9400002)"); n != 0 {
		t.Fatalf("дубль с негодной первой строкой записан: %d", n)
	}
	var rep struct {
		SkippedTimezone int `json:"skipped_timezone"`
	}
	if err := json.Unmarshal(journalRow(t, e.owner, res.ID).Reconciled, &rep); err != nil || rep.SkippedTimezone != 2 {
		t.Fatalf("отчёт: %v %+v", err, rep)
	}
}

// Строку журнала закрыл другой запуск (счёл зависшей), пока шла загрузка: итог не пишется,
// запись откатывается целиком, второго geo.import_finished нет.
func TestImportClosedByOtherRunRollsBack(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	raw := fixture(t)
	im := app.NewImporter(e.as, e.clk, source.FetchConfig{}, slog.New(slog.DiscardHandler))
	im.SetFetch(func(ctx context.Context, _ source.FetchConfig, _ string) (source.Raw, error) {
		tag, err := e.owner.Exec(ctx, `UPDATE geonames_imports SET status = 'failed', finished_at = now()
			WHERE country_code = 'RU' AND status = 'running'`)
		if err != nil || tag.RowsAffected() != 1 {
			return source.Raw{}, fmt.Errorf("закрыть свою строку: %v, строк %d", err, tag.RowsAffected())
		}
		return raw, nil
	})
	res, err := im.Import(ctx, "RU", nil, nil)
	if !errors.Is(err, app.ErrImportClosed) || res.ID == uuid.Nil {
		t.Fatalf("%v %+v", err, res)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places"); n != 0 {
		t.Fatalf("мест %d — запись не откатилась", n)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2",
		geo.EventImportFinished, res.ID); n != 0 {
		t.Fatalf("событий geo.import_finished %d — итог чужой строки переписан", n)
	}
	if j := journalRow(t, e.owner, res.ID); j.Status != "failed" || j.Error != nil || j.Places != nil {
		t.Fatalf("журнал тронут: %+v", j)
	}
}

// Предпочтительное, но вышедшее из употребления (to в прошлом) название не выбирается.
func TestImportIgnoresEndedNames(t *testing.T) {
	e := newEnv(t)
	raw := fixture(t)
	raw.Places = append(raw.Places, synth(9300001, 1)...)
	raw.AltNames = append(raw.AltNames,
		source.AltName{ID: 1, GeonameID: 9300001, Locale: "ru", Name: "Старое", Preferred: true, To: "1990"},
		source.AltName{ID: 2, GeonameID: 9300001, Locale: "ru", Name: "Новое"})
	if _, err := e.importer(&raw).Import(context.Background(), "RU", nil, nil); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := e.owner.QueryRow(context.Background(),
		"SELECT name FROM geonames_place_names WHERE geoname_id = 9300001 AND locale = 'ru'").Scan(&name); err != nil || name != "Новое" {
		t.Fatalf("%v %q", err, name)
	}
}

// addRegion — регион RU без geoname_admin1_code с ru-названием (как заведённый админом).
func addRegion(t *testing.T, e env, ru string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := e.owner.QueryRow(ctx, `INSERT INTO regions (country_id, name) SELECT id, $1 FROM countries WHERE code = 'RU'
		RETURNING id`, ru).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(ctx, `INSERT INTO region_names (region_id, locale, name) VALUES ($1, 'ru', $2)`, id, ru); err != nil {
		t.Fatal(err)
	}
	return id
}

// addCity — заведённый город без geoname_id; names — строки city_names (ru — зеркало name).
func addCity(t *testing.T, e env, region uuid.UUID, slug string, names map[string]string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := e.owner.QueryRow(ctx, `INSERT INTO cities (country_id, region_id, name, slug, timezone, centroid)
		SELECT id, $1, $2, $3, 'Europe/Moscow', ST_SetSRID(ST_MakePoint(50, 50), 4326)::geography
		FROM countries WHERE code = 'RU' RETURNING id`, region, names["ru"], slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for l, n := range names {
		if _, err := e.owner.Exec(ctx, `INSERT INTO city_names (city_id, locale, name) VALUES ($1, $2, $3)`, id, l, n); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestImportReconcilesCities(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var stavropolKrai uuid.UUID // регион сида с geoname_admin1_code = '70'
	if err := e.owner.QueryRow(ctx, `SELECT id FROM regions WHERE geoname_admin1_code = '70'`).Scan(&stavropolKrai); err != nil {
		t.Fatal(err)
	}
	ural := addRegion(t, e, "Свердловская область") // без кода: совпадение по названию admin1 71
	// одно совпадение: место 526815 (Михайловск, admin1 71); перевода en нет — появится
	mikh := addCity(t, e, ural, "test-mikhaylovsk-ural", map[string]string{"ru": "Михайловск"})
	// одно совпадение, en поправлен админом — не затирается
	lesnoy := addCity(t, e, ural, "test-lesnoy", map[string]string{"ru": "Лесной", "en": "Lesnoy (admin)"})
	// два места-тёзки в одном регионе — решает админ
	zar := addCity(t, e, stavropolKrai, "test-zarechnyy", map[string]string{"ru": "Заречный"})
	// два заведённых города-тёзки на одно место: привязать нельзя ни один (второй дал бы 23505
	// cities_geoname_id_key и уронил импорт страны) — решает админ
	sosA := addCity(t, e, stavropolKrai, "test-sosnovka-a", map[string]string{"ru": "Сосновка"})
	sosB := addCity(t, e, stavropolKrai, "test-sosnovka-b", map[string]string{"ru": "Сосновка"})
	// место есть, но уже привязано к Ставрополю сида — не кандидат
	addCity(t, e, stavropolKrai, "test-stavropol-2", map[string]string{"ru": "Ставрополь"})
	// совпадений нет
	addCity(t, e, stavropolKrai, "test-nigdeevsk", map[string]string{"ru": "Нигдеевск"})

	raw := fixture(t)
	named(&raw, 9100001, "Zarechnyy", "Заречный", "70")
	named(&raw, 9100002, "Zarechnyy", "Заречный", "70")
	named(&raw, 9100010, "Lesnoy", "Лесной", "71")
	named(&raw, 9100020, "Sosnovka", "Сосновка", "70")
	im := e.importer(&raw)
	res, err := im.Import(ctx, "RU", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := res.Reconciled
	if len(rec.Linked) != 2 || rec.Linked[0].Slug != "test-lesnoy" || rec.Linked[0].GeonameID != 9100010 ||
		rec.Linked[1].Slug != "test-mikhaylovsk-ural" || rec.Linked[1].GeonameID != 526815 {
		t.Fatalf("привязаны: %+v", rec.Linked)
	}
	// порядок — по slug (ImportUnlinkedCities)
	if len(rec.Ambiguous) != 3 ||
		rec.Ambiguous[0].CityID != sosA || !slices.Equal(rec.Ambiguous[0].Candidates, []int64{9100020}) ||
		rec.Ambiguous[1].CityID != sosB || !slices.Equal(rec.Ambiguous[1].Candidates, []int64{9100020}) ||
		rec.Ambiguous[2].CityID != zar || !slices.Equal(rec.Ambiguous[2].Candidates, []int64{9100001, 9100002}) {
		t.Fatalf("неоднозначные: %+v", rec.Ambiguous)
	}
	if len(rec.NotFound) != 2 || rec.NotFound[0].Slug != "test-nigdeevsk" || rec.NotFound[1].Slug != "test-stavropol-2" {
		t.Fatalf("не найдены: %+v", rec.NotFound)
	}
	city := func(id uuid.UUID) (gid *int64, version int64) {
		if err := e.owner.QueryRow(ctx, "SELECT geoname_id, version FROM cities WHERE id = $1", id).Scan(&gid, &version); err != nil {
			t.Fatal(err)
		}
		return gid, version
	}
	if gid, v := city(mikh); gid == nil || *gid != 526815 || v != 2 {
		t.Fatalf("Михайловск: %v v%d", gid, v)
	}
	if gid, v := city(lesnoy); gid == nil || *gid != 9100010 || v != 2 {
		t.Fatalf("Лесной: %v v%d", gid, v)
	}
	for name, id := range map[string]uuid.UUID{"Заречный": zar, "Сосновка-а": sosA, "Сосновка-б": sosB} {
		if gid, v := city(id); gid != nil || v != 1 {
			t.Fatalf("%s тронут: %v v%d", name, gid, v)
		}
	}
	names := pairs(t, e.owner, `SELECT c.slug || '/' || n.locale, n.name FROM city_names n JOIN cities c ON c.id = n.city_id
		WHERE c.slug LIKE 'test-%'`)
	if names["test-mikhaylovsk-ural/en"] != "Mikhaylovsk" || names["test-lesnoy/en"] != "Lesnoy (admin)" ||
		names["test-mikhaylovsk-ural/ru"] != "Михайловск" {
		t.Fatalf("переводы: %v", names)
	}
	var code *string
	if err := e.owner.QueryRow(ctx, "SELECT geoname_admin1_code FROM regions WHERE id = $1", ural).Scan(&code); err != nil ||
		code == nil || *code != "71" {
		t.Fatalf("код региона: %v %v", err, code)
	}
	regionNames := pairs(t, e.owner, fmt.Sprintf(`SELECT locale, name FROM region_names WHERE region_id = '%s'`, ural))
	if regionNames["ru"] != "Свердловская область" || regionNames["en"] != "Sverdlovsk Oblast" {
		t.Fatalf("названия региона: %v", regionNames)
	}
	var payload []byte
	var aggVersion int64
	if err := e.owner.QueryRow(ctx, `SELECT payload, aggregate_version FROM outbox WHERE event_type = $1 AND aggregate_id = $2`,
		geo.EventCityLinked, mikh).Scan(&payload, &aggVersion); err != nil {
		t.Fatal(err)
	}
	var linked geo.CityLinked
	if err := json.Unmarshal(payload, &linked); err != nil || linked.CityID != mikh || linked.GeonameID != 526815 || aggVersion != 2 {
		t.Fatalf("geo.city_linked: %s v%d %v", payload, aggVersion, err)
	}
	if rep := journalRow(t, e.owner, res.ID).Reconciled; !bytes.Contains(rep, []byte("test-zarechnyy")) ||
		!bytes.Contains(rep, []byte(`"conflict": []`)) {
		t.Fatalf("отчёт сверки в журнале (conflict — пустой список, не null): %s", rep)
	}
	// повтор: привязанные уже не кандидаты, событий не прибавилось
	res, err = im.Import(ctx, "RU", nil, nil)
	if err != nil || len(res.Reconciled.Linked) != 0 || len(res.Reconciled.Ambiguous) != 3 {
		t.Fatalf("повтор: %v %+v", err, res.Reconciled)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM outbox WHERE event_type = $1", geo.EventCityLinked); n != 2 {
		t.Fatalf("событий geo.city_linked %d", n)
	}
}

func admin1Without(list []source.Admin1, keep func(source.Admin1) bool) []source.Admin1 {
	return slices.DeleteFunc(slices.Clone(list), func(a source.Admin1) bool { return !keep(a) })
}

// R34 (а): каждый код admin1 отобранных мест (кроме «00» — «неизвестно» в GeoNames — и пустого)
// есть в admin1 страны из источника; иначе импорт падает до любой записи.
func TestImportRequiresAdmin1OfPlaces(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := fixture(t)
	odd := synth(9500001, 2)
	odd[0].Admin1Code, odd[1].Admin1Code = "00", ""
	base.Places = append(base.Places, odd...)
	raw := base
	im := e.importer(&raw)
	first, err := im.Import(ctx, "RU", nil, nil)
	if err != nil || first.PlacesUpserted != 5 {
		t.Fatalf("места с кодом «00» и пустым не требуют admin1: %v %+v", err, first)
	}
	raw.Admin1 = admin1Without(base.Admin1, func(a source.Admin1) bool { return a.Code != "70" })
	e.clk.Advance(time.Hour)
	res, err := im.Import(ctx, "RU", nil, nil)
	if !errors.Is(err, app.ErrAdmin1Incomplete) || !strings.HasSuffix(err.Error(), ": 70") {
		t.Fatalf("err = %v", err)
	}
	if j := journalRow(t, e.owner, res.ID); j.Status != "failed" || j.Error == nil || !strings.HasSuffix(*j.Error, ": 70") {
		t.Fatalf("журнал: %+v", j)
	}
	if s := finishedStatus(t, e.owner, res.ID); s != "failed" {
		t.Fatalf("событие: %s", s)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_admin1"); n != 3 {
		t.Fatalf("admin1 %d — данные тронуты", n)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places WHERE import_id = $1", first.ID); n != 5 {
		t.Fatalf("мест первого импорта %d — данные тронуты", n)
	}
}

// R34 (б): строк admin1 страны в источнике не меньше 90 % от строк geonames_admin1 в базе —
// обрезанный на границе строки admin1CodesASCII.txt не удаляет регионы страны.
func TestImportRejectsTruncatedAdmin1(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := fixture(t)
	raw := base
	im := e.importer(&raw)
	if _, err := im.Import(ctx, "RU", nil, nil); err != nil {
		t.Fatal(err)
	}
	// места — только региона 70 (иначе раньше сработает проверка ссылок) и не меньше прежних
	raw = without(base, 526815)
	raw.Places = append(raw.Places, synth(9000001, 2)...)
	raw.Admin1 = admin1Without(base.Admin1, func(a source.Admin1) bool { return a.Code == "70" }) // 1 из 3
	e.clk.Advance(time.Hour)
	res, err := im.Import(ctx, "RU", nil, nil)
	if !errors.Is(err, app.ErrSourceTruncated) || !strings.Contains(err.Error(), "admin1") {
		t.Fatalf("err = %v", err)
	}
	if j := journalRow(t, e.owner, res.ID); j.Status != "failed" || j.Error == nil || !strings.Contains(*j.Error, "admin1") {
		t.Fatalf("журнал: %+v", j)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_admin1"); n != 3 {
		t.Fatalf("admin1 %d — регионы удалены", n)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_admin1_names"); n != 6 {
		t.Fatalf("названий admin1 %d", n)
	}
}

// R35: гонка с активацией того же места админом — привязка откатывается до своего savepoint,
// город — в Conflict, остальная сверка и запись источника проходят, импорт успешен.
func TestImportReconcileConflictKeepsImport(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ural := addRegion(t, e, "Свердловская область")
	mikh := addCity(t, e, ural, "test-mikhaylovsk-ural", map[string]string{"ru": "Михайловск"})
	lesnoy := addCity(t, e, ural, "test-lesnoy", map[string]string{"ru": "Лесной"})
	// город, которому «админ» отдаёт место Михайловска между отбором кандидатов и привязкой
	other := addCity(t, e, ural, "test-activated-by-admin", map[string]string{"ru": "Активированный"})
	raw := fixture(t)
	named(&raw, 9100010, "Lesnoy", "Лесной", "71")
	im := e.importer(&raw)
	im.SetBeforeLink(func(_ context.Context, cityID uuid.UUID, gid int64) {
		if cityID != mikh {
			return
		}
		// не t.Fatal: колбэк внутри db.InTx держит соединение
		if _, err := e.owner.Exec(context.Background(), "UPDATE cities SET geoname_id = $1 WHERE id = $2", gid, other); err != nil {
			t.Errorf("активация админом: %v", err)
		}
	})
	res, err := im.Import(ctx, "RU", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := res.Reconciled
	if len(rec.Conflict) != 1 || rec.Conflict[0].CityID != mikh || !slices.Equal(rec.Conflict[0].Candidates, []int64{526815}) {
		t.Fatalf("конфликт: %+v", rec.Conflict)
	}
	if len(rec.Linked) != 1 || rec.Linked[0].CityID != lesnoy || rec.Linked[0].GeonameID != 9100010 {
		t.Fatalf("привязаны: %+v", rec.Linked)
	}
	j := journalRow(t, e.owner, res.ID)
	var rep struct {
		Conflict []struct {
			Slug       string  `json:"slug"`
			Candidates []int64 `json:"candidates"`
		} `json:"conflict"`
	}
	if err := json.Unmarshal(j.Reconciled, &rep); err != nil || j.Status != "succeeded" || len(rep.Conflict) != 1 ||
		rep.Conflict[0].Slug != "test-mikhaylovsk-ural" || !slices.Equal(rep.Conflict[0].Candidates, []int64{526815}) {
		t.Fatalf("журнал: %v %s %s", err, j.Status, j.Reconciled)
	}
	var gid *int64
	var version int64
	if err := e.owner.QueryRow(ctx, "SELECT geoname_id, version FROM cities WHERE id = $1", mikh).Scan(&gid, &version); err != nil ||
		gid != nil || version != 1 {
		t.Fatalf("Михайловск тронут: %v %v v%d", err, gid, version)
	}
	if n := count(t, e.owner, "SELECT count(*) FROM city_names WHERE city_id = $1", mikh); n != 1 {
		t.Fatalf("переводов Михайловска %d — откат неполный", n)
	}
	for id, want := range map[uuid.UUID]int{mikh: 0, lesnoy: 1} {
		if n := count(t, e.owner, "SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2",
			geo.EventCityLinked, id); n != want {
			t.Fatalf("geo.city_linked для %s: %d, ждали %d", id, n, want)
		}
	}
	if n := count(t, e.owner, "SELECT count(*) FROM geonames_places WHERE import_id = $1", res.ID); n != 4 {
		t.Fatalf("мест %d", n)
	}
	if n := count(t, e.owner, `SELECT (after->>'cities_conflict')::int FROM audit_log WHERE action = 'geo.import' AND object_id = $1`,
		res.ID); n != 1 {
		t.Fatalf("аудит cities_conflict: %d", n)
	}
}
