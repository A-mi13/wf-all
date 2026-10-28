package queue_test

import (
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"wf/backend/migrations"
)

// riverFile — наша миграция схемы River: 00NN_river_v<K>.sql (сгенерирована `river migrate-get --version K`).
var riverFile = regexp.MustCompile(`^\d{4}_river_v(\d+)\.sql$`)

// ourRiverVersions — версии линии River, которые покрывают наши миграции goose.
func ourRiverVersions(t *testing.T) []int {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, e := range entries {
		if m := riverFile.FindStringSubmatch(e.Name()); m != nil {
			v, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// noopUp — миграция River, пустая для Postgres: без комментариев только `SELECT 1;` (v8: «No-op. Postgres sequences
// already prevent…» — накатывать нечего, поэтому своего файла у неё нет).
func noopUp(sql string) bool {
	var body []string
	for line := range strings.Lines(sql) {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "--") {
			body = append(body, l)
		}
	}
	return strings.Join(body, " ") == "SELECT 1;"
}

// riverVersionV1 — v1 линии River создаёт её таблицу версий river_migration; наши версии ведёт goose, поэтому v1 у
// нас нет (линия начинается с 0011_river_v2.sql).
const riverVersionV1 = 1

// uncovered — версии линии River (кроме v1), которых нет в ours и которые что-то меняют в схеме; и наши версии,
// которых в линии River нет.
func uncovered(line []rivermigrate.Migration, ours []int) (missing, unknown []int) {
	known := make([]int, 0, len(line))
	for _, m := range line {
		known = append(known, m.Version)
		if m.Version != riverVersionV1 && !slices.Contains(ours, m.Version) && !noopUp(m.SQLUp) {
			missing = append(missing, m.Version)
		}
	}
	for _, v := range ours {
		if !slices.Contains(known, v) {
			unknown = append(unknown, v)
		}
	}
	return missing, unknown
}

// Страж подъёма River (m0-1 ревью G2): у River появилась миграция схемы (новая колонка river_job и т. п.), а нашего
// 00NN_river_v<K>.sql нет — стражи обслуживания и бинарников зелёные, пока ни один тест не дёрнет изменённый SQL
// River, и ошибку увидел бы стенд. Линия — через API River (rivermigrate.AllVersions драйвера riverpgxv5), не
// чтением файлов модуля. Красный — сгенерировать недостающее: из backend/
// go tool -modfile=tools/go.mod river migrate-get --version K --up|--down → migrations/00NN_river_vK.sql
// (номер — следующий свободный, README миграций).
func TestRiverMigrationLineCovered(t *testing.T) {
	m, err := rivermigrate.New(riverpgxv5.New(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	line := m.AllVersions()
	ours := ourRiverVersions(t)
	if len(line) == 0 || len(ours) == 0 {
		t.Fatalf("линия River %d версий, наших файлов %d — разбор пуст?", len(line), len(ours))
	}
	missing, unknown := uncovered(line, ours)
	if len(missing) > 0 {
		t.Errorf("у River есть миграции схемы v%v, а наших 00NN_river_v<K>.sql нет (наши: %v)", missing, ours)
	}
	if len(unknown) > 0 {
		t.Errorf("наши миграции River v%v не из линии riverpgxv5 (последняя — v%d)", unknown, line[len(line)-1].Version)
	}
}

// Сам страж не вхолостую: новая версия линии с изменением схемы — пропущена; пустая — нет; наша версия вне линии —
// неизвестна.
func TestRiverMigrationUncovered(t *testing.T) {
	line := []rivermigrate.Migration{
		{Version: 1, SQLUp: "CREATE TABLE river_migration ();"},
		{Version: 2, SQLUp: "CREATE TABLE river_job ();"},
		{Version: 3, SQLUp: "-- No-op.\nSELECT 1;\n"},
		{Version: 4, SQLUp: "ALTER TABLE river_job ADD COLUMN x int;"},
	}
	missing, unknown := uncovered(line, []int{2, 5})
	if !slices.Equal(missing, []int{4}) || !slices.Equal(unknown, []int{5}) {
		t.Fatalf("пропущены %v, неизвестны %v; ждали [4] и [5]", missing, unknown)
	}
}
