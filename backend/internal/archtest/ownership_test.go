package archtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wf/backend/internal/archtest/sqlscan"
	"wf/backend/internal/platform/testkit/dbtest"
)

// Подсадка багов: нарушения §4.1–4.2 на синтетическом SQL модуля.
func TestOwnershipViolations(t *testing.T) {
	ok := []struct {
		module string
		u      sqlscan.Usage
	}{
		{"teams", sqlscan.Usage{Writes: []string{"teams"}, Reads: []string{"team_members"}}},
		{"matches", sqlscan.Usage{Reads: []string{"geo_read_city_settings"}, Functions: []string{"nearby_pitches", "age_years", "count"}}},
		{"platform", sqlscan.Usage{Writes: []string{"river_job"}}},
		{"matches", sqlscan.Usage{Functions: []string{"matches_set_slot"}}},
		// geo пишет свои таблицы источника и справочника (спека geo §2)
		{"geo", sqlscan.Usage{Writes: []string{"city_names", "geonames_places", "geonames_imports"}, Reads: []string{"countries", "geonames_admin1_names"}}},
		// версии приложения — таблица платформы
		{"platform", sqlscan.Usage{Writes: []string{"app_versions"}}},
	}
	for _, c := range ok {
		if v := ownershipViolations(c.module, c.u); len(v) != 0 {
			t.Errorf("%s %+v: лишние нарушения %v", c.module, c.u, v)
		}
	}
	bad := []struct {
		module string
		u      sqlscan.Usage
	}{
		{"matches", sqlscan.Usage{Writes: []string{"teams"}}},                // запись в чужую таблицу
		{"matches", sqlscan.Usage{Reads: []string{"pitches"}}},               // чтение чужой таблицы
		{"teams", sqlscan.Usage{Writes: []string{"geo_read_city_settings"}}}, // запись в представление
		{"teams", sqlscan.Usage{Reads: []string{"no_such_table"}}},           // объект без владельца
		{"teams", sqlscan.Usage{Writes: []string{"no_such_table"}}},          // объект без владельца
		{"teams", sqlscan.Usage{Functions: []string{"matches_set_slot"}}},    // не экспортированная функция
		{"identity", sqlscan.Usage{Reads: []string{"city_names"}}},           // таблица geo в обход представления
		{"geo", sqlscan.Usage{Writes: []string{"app_versions"}}},             // таблица платформы
		{"teams", sqlscan.Usage{Reads: []string{"city_settings"}}},           // удалённое в 0019 представление
	}
	for _, c := range bad {
		if v := ownershipViolations(c.module, c.u); len(v) == 0 {
			t.Errorf("%s %+v: нарушение не поймано", c.module, c.u)
		}
	}
}

func TestSchemaOwnersAreKnownModules(t *testing.T) {
	for name, o := range Schema {
		if !isKnownOwner(o.Owner) {
			t.Errorf("%s: владелец %q не модуль из Layers и не platform", name, o.Owner)
		}
		if o.Kind == View && o.Exported && !strings.HasPrefix(name, o.Owner+"_read_") {
			t.Errorf("%s: экспортированное представление называется %s_read_<имя> (спека §4.2)", name, o.Owner)
		}
	}
}

const schemaObjectsSQL = `
SELECT CASE WHEN c.relkind IN ('v', 'm') THEN 'view' ELSE 'table' END, c.relname
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
UNION ALL
SELECT 'function', p.proname
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
 WHERE n.nspname = 'public'
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')`

// Каждая таблица, представление и функция мигрированной схемы (кроме объектов
// расширений) имеет владельца, и в карте нет записей об объектах, которых уже нет.
func TestEverySchemaObjectHasOwner(t *testing.T) {
	pool := dbtest.NewPool(t)
	rows, err := pool.Query(context.Background(), schemaObjectsSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	kinds := map[string]Kind{"table": Table, "view": View, "function": Function}
	inDB := map[string]bool{}
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			t.Fatal(err)
		}
		inDB[name] = true
		o, ok := ownerOf(name)
		if !ok {
			t.Errorf("%s %s: нет владельца — добавь в archtest.Schema (спека §4.1)", kind, name)
			continue
		}
		if _, inMap := Schema[name]; inMap && o.Kind != kinds[kind] {
			t.Errorf("%s: в карте вид %v, в базе %s", name, o.Kind, kind)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(inDB) < 50 {
		t.Fatalf("в схеме всего %d объектов — база не мигрирована, страж проверяет вхолостую", len(inDB))
	}
	for name := range Schema {
		if !inDB[name] {
			t.Errorf("%s: есть в archtest.Schema, но нет в схеме — убери устаревшую запись", name)
		}
	}
}

// Главный страж: SQL каждого модуля трогает только своё и экспортированное.
func TestModuleQueriesRespectOwnership(t *testing.T) {
	var files []string
	for _, g := range queryGlobs {
		found, err := filepath.Glob("../" + g)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, found...)
	}
	sawFlags := false
	for _, f := range files {
		slash := filepath.ToSlash(f)
		module := strings.Split(strings.TrimPrefix(slash, "../"), "/")[0]
		if strings.HasPrefix(slash, "../platform/") {
			module = "platform"
		}
		if strings.Contains(slash, "platform/flags/") {
			sawFlags = true
		}
		src, err := os.ReadFile(f) //nolint:gosec // путь — из glob по каталогам queries этого репо, не внешний ввод
		if err != nil {
			t.Fatal(err)
		}
		u, err := sqlscan.Scan(string(src))
		if err != nil {
			t.Errorf("%s: %v", slash, err)
			continue
		}
		for _, v := range ownershipViolations(module, u) {
			t.Errorf("%s (модуль %s): %s", slash, module, v)
		}
	}
	if !sawFlags {
		t.Fatal("не найден internal/platform/flags/queries — страж проверяет вхолостую")
	}
}

// Каталоги queries из backend/sqlc.yaml лежат там, где их видит страж владения: SQL вне
// queryGlobs выпал бы из проверки молча.
func TestSqlcQueriesCoveredByOwnershipGuard(t *testing.T) {
	cfg, err := os.ReadFile("../../sqlc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dirs, violations, err := sqlcQueriesViolations(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if dirs == 0 {
		t.Fatal("в sqlc.yaml нет ни одного queries — страж проверяет вхолостую")
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// Подсадка багов: queries вне internal/<модуль>/queries/ и internal/platform/<пакет>/queries/.
func TestSqlcQueriesViolations(t *testing.T) {
	ok := []string{
		"sql:\n  - queries: internal/platform/flags/queries/\n",
		"sql:\n  - queries: internal/teams/queries\n",
		"sql:\n  - queries: [internal/teams/queries/, ./internal/matches/queries/]\n",
	}
	for _, cfg := range ok {
		if _, v, err := sqlcQueriesViolations([]byte(cfg)); err != nil || len(v) != 0 {
			t.Errorf("%q: лишние нарушения %v, err %v", cfg, v, err)
		}
	}
	bad := []string{
		"sql:\n  - queries: internal/teams/internal/store/queries/\n", // глубже queryGlobs
		"sql:\n  - queries: internal/nosuch/queries/\n",               // не модуль из Layers
		"sql:\n  - queries: internal/httpapi/queries/\n",              // не модуль и не platform
		"sql:\n  - queries: internal/platform/queries/\n",             // platform без пакета
		"sql:\n  - queries: queries/\n",                               // вне internal/
		"sql:\n  - queries: internal/teams/queries/teams.sql\n",       // файл, а не каталог
		"sql:\n  - queries: [internal/teams/queries/, internal/teams/store/queries/]\n",
		"sql:\n  - engine: postgresql\n", // элемент без queries
	}
	for _, cfg := range bad {
		if _, v, err := sqlcQueriesViolations([]byte(cfg)); err == nil && len(v) == 0 {
			t.Errorf("%q: нарушение не поймано", cfg)
		}
	}
}
