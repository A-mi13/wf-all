// Package sqlmirror — страж пар запросов X/StaffX в файле sqlc (волна 2 плана A, HTTP непредусмотренный 2;
// план B, Task 3). У admin-api свои таблицы механизмов платформы (staff_idempotency_keys,
// staff_rate_limits): права на них только у роли admin. sqlc не параметризует имя таблицы, поэтому
// каждый запрос пишется дважды — X над таблицей игроков и StaffX над таблицей сотрудников. Пара обязана
// совпадать текстом после замены имени таблицы: правка X без StaffX молча развела бы семантику public и
// admin.
package sqlmirror

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

type query struct{ kind, body string }

var nameLine = regexp.MustCompile(`^-- name: (\w+) (:\w+)\s*$`)

// Check — в файле file из fsys каждый запрос над table имеет пару Staff<имя> того же вида, который обращается к
// staffTable и равен оригиналу после замены в нём table → staffTable (комментарии отброшены, пробелы схлопнуты);
// у каждого Staff-запроса есть оригинал, оригинал к staffTable не обращается. minPairs — сколько пар обязано найтись (страж не вхолостую).
func Check(t testing.TB, fsys fs.FS, file, table, staffTable string, minPairs int) {
	t.Helper()
	src, err := fs.ReadFile(fsys, file)
	if err != nil {
		t.Fatal(err)
	}
	queries := parse(t, string(src))
	pairs := 0
	for name, q := range queries {
		if base, ok := strings.CutPrefix(name, "Staff"); ok {
			orig, ok := queries[base]
			if !ok {
				t.Errorf("%s без пары %s", name, base)
				continue
			}
			pairs++
			if q.kind != orig.kind {
				t.Errorf("%s %s, а %s %s", name, q.kind, base, orig.kind)
			}
			if !strings.Contains(q.body, staffTable) {
				t.Errorf("%s не обращается к %s", name, staffTable)
			}
			// Замена в прямую сторону (m2 ревью G1): оригинал с таблицей сотрудников вместо таблицы игроков == StaffX.
			// Обратная (staff → игроков) склеивала с оригиналом «смешанный» StaffX, читающий обе таблицы.
			if want := strings.ReplaceAll(orig.body, table, staffTable); want != q.body {
				t.Errorf("%s расходится с %s после замены таблицы:\n%s\n---\n%s", name, base, q.body, want)
			}
			continue
		}
		if strings.Contains(q.body, staffTable) {
			// в оригинале прямая замена неоднозначна (staff_ + staff_…) — и таблица сотрудников ему не положена
			t.Errorf("%s обращается к %s, а это запрос над %s", name, staffTable, table)
		}
		if strings.Contains(q.body, table) {
			if _, ok := queries["Staff"+name]; !ok {
				t.Errorf("%s обращается к %s, а пары Staff%s нет", name, table, name)
			}
		}
	}
	if pairs < minPairs {
		t.Fatalf("пар %d, ждали не меньше %d — разбор файла пуст?", pairs, minPairs)
	}
}

// parse — запросы sqlc файла: имя → вид и текст без комментариев, пробелы схлопнуты.
func parse(t testing.TB, src string) map[string]query {
	t.Helper()
	out := map[string]query{}
	var name, kind string
	var body []string
	flush := func() {
		if name != "" {
			if _, dup := out[name]; dup {
				t.Fatalf("запрос %s дважды", name)
			}
			out[name] = query{kind: kind, body: strings.Join(strings.Fields(strings.Join(body, " ")), " ")}
		}
	}
	for line := range strings.Lines(strings.ReplaceAll(src, "\r\n", "\n")) {
		line = strings.TrimRight(line, "\n")
		if m := nameLine.FindStringSubmatch(line); m != nil {
			flush()
			name, kind, body = m[1], m[2], nil
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		body = append(body, line)
	}
	flush()
	return out
}
