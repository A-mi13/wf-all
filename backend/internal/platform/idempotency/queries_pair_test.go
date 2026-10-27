package idempotency_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Волна 2, HTTP непредусмотренный 2: ключи admin-api — staff_idempotency_keys, запросы — пары X/StaffX в
// queries/idempotency.sql (sqlc не параметризует имя таблицы). Пара обязана совпадать текстом после замены имени
// таблицы: правка Claim без StaffClaim (срок ключа, условие переиспользования) молча развела бы семантику
// идемпотентности public и admin. Каждый запрос над idempotency_keys — с парой, каждая пара — с оригиналом.
func TestStaffQueriesMirrorPublic(t *testing.T) {
	src, err := os.ReadFile("queries/idempotency.sql")
	if err != nil {
		t.Fatal(err)
	}
	queries := parseQueries(t, string(src))
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
			if !strings.Contains(q.body, "staff_idempotency_keys") {
				t.Errorf("%s не обращается к staff_idempotency_keys", name)
			}
			if got := strings.ReplaceAll(q.body, "staff_idempotency_keys", "idempotency_keys"); got != orig.body {
				t.Errorf("%s расходится с %s после замены таблицы:\n%s\n---\n%s", name, base, got, orig.body)
			}
			continue
		}
		if strings.Contains(q.body, "idempotency_keys") {
			if _, ok := queries["Staff"+name]; !ok {
				t.Errorf("%s обращается к idempotency_keys, а пары Staff%s нет", name, name)
			}
		}
	}
	// страж не вхолостую: пары Get, Claim, SaveResponse, DeleteExpired
	if pairs < 4 {
		t.Fatalf("пар %d, ждали не меньше 4 — разбор файла пуст?", pairs)
	}
}

type sqlQuery struct{ kind, body string }

var nameLine = regexp.MustCompile(`^-- name: (\w+) (:\w+)\s*$`)

// parseQueries — запросы sqlc файла: имя → вид и текст без комментариев, пробелы схлопнуты.
func parseQueries(t *testing.T, src string) map[string]sqlQuery {
	t.Helper()
	out := map[string]sqlQuery{}
	var name, kind string
	var body []string
	flush := func() {
		if name != "" {
			if _, dup := out[name]; dup {
				t.Fatalf("запрос %s дважды", name)
			}
			out[name] = sqlQuery{kind: kind, body: strings.Join(strings.Fields(strings.Join(body, " ")), " ")}
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
