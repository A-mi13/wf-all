package queue_test

import (
	"errors"
	"fmt"
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
	t.Parallel()
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
	t.Parallel()
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

// Хвост У1 — дрейф текста: исполняемый SQL нашего 00NN_river_vK.sql совпадает с миграцией vK линии riverpgxv5
// текущей версии River (rivermigrate.AllVersions — тот же SQL, что печатает `river migrate-get --version K
// --up|--down`; шаблон схемы — пустой, как у migrate-get без --schema). Сравниваются строки без строк-комментариев и
// пустых: комментарии River правит и в выпущенных версиях (v0.50: «PostgreSQL» → «Postgres» в v7), а слитую миграцию
// goose ради комментария не правят. River поправил SQL выпущенной версии (или файл правили руками) — красный: базам с
// прежним текстом нужна новая миграция goose; файл ещё не слит в main — сгенерировать заново той же командой.
func TestRiverMigrationTextMatchesRiver(t *testing.T) {
	t.Parallel()
	m, err := rivermigrate.New(riverpgxv5.New(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	line := map[int]rivermigrate.Migration{}
	for _, v := range m.AllVersions() {
		line[v.Version] = v
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		g := riverFile.FindStringSubmatch(e.Name())
		if g == nil {
			continue
		}
		v, _ := strconv.Atoi(g[1])
		river, ok := line[v]
		if !ok {
			continue // версия вне линии — забота TestRiverMigrationLineCovered
		}
		raw, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		up, down, err := gooseRiverSections(strings.ReplaceAll(string(raw), "\r\n", "\n"))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		for _, c := range []struct{ dir, ours, sql string }{{"up", up, river.SQLUp}, {"down", down, river.SQLDown}} {
			want, err := migrateGetText(v, c.dir, c.sql)
			if err != nil {
				t.Errorf("%s %s: %v", e.Name(), c.dir, err)
				continue
			}
			if sqlLines(c.ours) != sqlLines(want) {
				t.Errorf("%s [%s] расходится с River v%d: перегенерировать (river migrate-get --version %d --%s)\nнаш:\n%s\nRiver:\n%s",
					e.Name(), c.dir, v, v, c.dir, c.ours, want)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("ни одного 00NN_river_vK.sql не сверено — страж вхолостую")
	}
}

// Сам страж не вхолостую: изменённая строка SQL и строка, которой у нас нет, — расхождение; шаблон схемы снимается,
// другой комментарий и пустые строки — не расхождение.
func TestRiverMigrationTextCatchesDrift(t *testing.T) {
	t.Parallel()
	file := "-- шапка\n\n-- +goose Up\n-- +goose StatementBegin\n-- River main migration 002 [up]\nCREATE TABLE river_job ();\n" +
		"-- +goose StatementEnd\n\n-- +goose Down\n-- +goose StatementBegin\n-- River main migration 002 [down]\nDROP TABLE river_job;\n-- +goose StatementEnd\n"
	up, down, err := gooseRiverSections(file)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := migrateGetText(2, "up", "\n-- другой комментарий\n\nCREATE TABLE /* TEMPLATE: schema */river_job ();\n"); sqlLines(up) != sqlLines(want) {
		t.Fatalf("up расходится (комментарий и шаблон — не дрейф): %q против %q", up, want)
	}
	if want, _ := migrateGetText(2, "down", "DROP TABLE river_job CASCADE;"); sqlLines(down) == sqlLines(want) {
		t.Fatal("изменённая строка down не замечена")
	}
	if want, _ := migrateGetText(2, "up", "CREATE TABLE river_job ();\nSELECT 1;"); sqlLines(up) == sqlLines(want) {
		t.Fatal("строка, которой у нас нет, не замечена")
	}
	if _, err := migrateGetText(2, "up", "/* TEMPLATE_BEGIN: x */"); err == nil {
		t.Fatal("незнакомый шаблон River пропущен молча")
	}
}

// sqlLines — исполняемые строки SQL: без пустых и строк-комментариев, без пробелов справа.
func sqlLines(sql string) string {
	var out []string
	for line := range strings.Lines(sql) {
		l := strings.TrimRight(line, " \t\r\n")
		if s := strings.TrimSpace(l); s == "" || strings.HasPrefix(s, "--") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// gooseRiverSections — тексты между StatementBegin и StatementEnd секций Up и Down нашего файла River.
func gooseRiverSections(file string) (up, down string, err error) {
	section := func(rest, marker string) (string, string, error) {
		i := strings.Index(rest, marker+"\n-- +goose StatementBegin\n")
		if i < 0 {
			return "", "", errors.New("нет «" + marker + "» со StatementBegin")
		}
		rest = rest[i+len(marker+"\n-- +goose StatementBegin\n"):]
		j := strings.Index(rest, "\n-- +goose StatementEnd")
		if j < 0 {
			return "", "", errors.New("нет StatementEnd после «" + marker + "»")
		}
		return rest[:j], rest[j:], nil
	}
	up, rest, err := section(file, "-- +goose Up")
	if err != nil {
		return "", "", err
	}
	down, _, err = section(rest, "-- +goose Down")
	return up, down, err
}

// migrateGetText — текст `river migrate-get --version v --<dir>` без --schema: комментарий версии и SQL River без
// пробелов по краям, шаблон схемы — пустой. Иной шаблон River — ошибка (рендер migrate-get пришлось бы повторить).
func migrateGetText(v int, dir, sql string) (string, error) {
	sql = strings.ReplaceAll(sql, "/* TEMPLATE: schema */", "")
	if strings.Contains(sql, "/* TEMPLATE") {
		return "", errors.New("в SQL River незнакомый шаблон — сверить с рендером river migrate-get")
	}
	return fmt.Sprintf("-- River main migration %03d [%s]\n%s", v, dir, strings.TrimSpace(sql)), nil
}
