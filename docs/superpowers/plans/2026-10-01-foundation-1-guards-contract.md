# Фундамент 1/3: стражи модулей и контракт — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Автоматические стражи границ модулей (импорты, владение таблицами) и контракт, разбитый по модулям, с проверкой запросов и конвенций — до того, как появится первый модуль.

**Architecture:** Стражи живут в `backend/internal/archtest` (тесты на реальном дереве пакетов и реальной схеме) и проверяются подсадкой бага через чистые функции. SQL разбирается `wasilibs/go-pgquery` (pg_query в wasm, без cgo). Контракт собирается redocly из `contracts/openapi/{public,admin}/` в коммитящиеся бандлы; Go-типы — в отдельный пакет `oapi`; запросы валидируются middleware на kin-openapi.

**Tech Stack:** Go 1.27.1, kin-openapi 0.149.0, go-pgquery v0.0.0-20250409022910-10ac41983c07, @redocly/cli 2.57.0, oasdiff v1.32.1, yaml 2.9.1, vitest (catalog).

**Spec:** `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` — §3, §4.1–4.4, §6.8, §8.1–8.3, §12 (стражи 1–5). Это первый из трёх планов «фундамента» (§13 п. 0): 2/3 — транзакции, события, очередь, аудит, гранты; 3/3 — токены, Principal, IP, rate limit, антибот, идемпотентность, почта, файлы, серверные локали.

## Global Constraints

- Go 1.27.1; модуль `wf/backend`; sqlc разбирает грамматику PostgreSQL 17 — синтаксис PG18 в SQL не использовать.
- TDD: сначала падающий тест, потом код; каждый страж проверяется подсадкой бага (тест на чистой функции с нарушением).
- Комментарии в коде — по-русски, идентификаторы — по-английски; переводы строк LF.
- Тесты бэкенда — `./task backend:test` (нужен Postgres: `./task infra:up`); один пакет — `go test ./internal/<пакет>/... -count=1` из `backend/`.
- `typecheck`, `lint`, `build` не запускать без явной просьбы пользователя; тесты — всегда.
- pnpm: два процесса одновременно не запускать; `pnpm install` — один, ни с чем параллельно. Новая версия моложе суток отклоняется `minimumReleaseAge` — ждать, исключений не добавлять.
- Коммиты: одна строка по-русски, без тела; только свои пути: `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A`, `git add .`, `git commit -a`, `git stash`, `git reset`, `git clean`, `git checkout -- .`.
- `rm -rf` вне проекта запрещён; временные файлы — в `.tools/tmp/` (в `.gitignore` вместе с `.tools/`), удалять поштучно `rm -f`.
- Сгенерированное коммитится; `./task backend:gen:check` сверяет.

## Review Focus

1. SQL с литералом или комментарием, содержащим `@` (`'a@b.ru'`), — препроцессор именованных параметров sqlc не должен ломать разбор (Task 1, тест «@ в строке»).
2. Схема, в которой расширение (PostGIS) создало свои таблицы и функции, — страж «у каждого объекта есть владелец» не должен требовать владельца для `spatial_ref_sys` и функций PostGIS (Task 3, фильтр `pg_depend.deptype = 'e'` + проверка на реальной базе).
3. `operationId` с подчёркиванием или дефисом — имя метода oapi-codegen перестаёт совпадать с `operationId` с заглавной буквы, страж тегов промолчит или соврёт; конвенция lowerCamel проверяется в контракте (Task 6, тест `snake_case`).
4. Запрос без `Content-Type` или с телом не-JSON на ручку с телом — должен быть `400 request.invalid` без утечки текста ошибки kin-openapi (Task 7, тесты «неверный Content-Type» и «битый JSON»).
5. Бандл, собранный на Windows, — переводы строк LF (`.gitattributes: * text=auto eol=lf`) и одинаковый вывод на Windows и Linux, иначе `gen:check` в CI покраснеет; проверяется сравнением бандла после `git add` (Task 4, шаг проверки `git diff --stat`).

## Карта файлов

```
backend/
  go.mod                                        + github.com/wasilibs/go-pgquery
  internal/archtest/
    sqlscan/scan.go, scan_test.go               Task 1 — что SQL пишет, читает, вызывает
    modules.go, modules_test.go                 Task 2 — слои модулей, пары intx, страж импортов
    ownership.go, ownership_test.go             Task 3 — владельцы объектов схемы, страж SQL
  internal/httpapi/{public,admin}/
    oapi-codegen.yaml                           Task 5 — генерация в подпакет oapi
    oapi/api.gen.go                             Task 5 — сгенерировано (старый api.gen.go удаляется)
    server.go, handler.go, handler_test.go      Task 5, Task 7
  internal/platform/testkit/apitest/tags.go, tags_test.go   Task 5 — страж «тег = модуль»
  internal/platform/httpx/
    problem.go                                  Task 7 — FieldError, errors в Problem
    router.go                                   Task 7 — NewRouter(log, mws...)
    validate.go, validate_test.go               Task 7 — LimitBody, ValidateRequests
  internal/platform/server/server.go, server_test.go        Task 7 — build возвращает (Handler, error)
  cmd/api/main.go, cmd/admin-api/main.go        Task 7
  Taskfile.yml                                  Task 4 — gen собирает бандл и генерит в oapi
contracts/
  openapi/public/root.yaml, public/platform/{paths,schemas}.yaml   Task 4, Task 6
  openapi/admin/root.yaml, admin/platform/{paths,schemas}.yaml     Task 4, Task 6
  openapi/public.yaml, admin.yaml               Task 4 — бандлы (сгенерировано)
  scripts/bundle.mjs                            Task 4
  src/conventions.mjs, src/error-codes.mjs, src/error-codes.d.mts  Task 6, Task 8
  test/bundle.test.mjs, test/conventions.test.mjs                  Task 4, Task 6
  package.json                                  Task 4, Task 8
packages/i18n/src/index.ts, index.test.ts       Task 8 — missingKeys
apps/{web,admin}/messages/{ru,en}.json          Task 8 — errors.*
apps/{web,admin}/src/i18n/error-codes.test.ts   Task 8
apps/{web,admin}/package.json                   Task 8 — devDependency @wf/contracts
tools/go.mod, scripts/bootstrap.sh              Task 9 — oasdiff
scripts/contracts-breaking.sh, Taskfile.yml, .github/workflows/backend.yml   Task 9
.prettierignore                                 Task 4
docs/04-принципы-архитектуры.md, docs/versions.md, .claude/rules/{backend,contracts}.md   Task 10
```

---

## Фаза A. Стражи бэкенда

### Task 1: Разбор SQL — что запрос пишет, читает и вызывает

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`
- Create: `backend/internal/archtest/sqlscan/scan.go`
- Test: `backend/internal/archtest/sqlscan/scan_test.go`

**Interfaces:**
- Consumes: —
- Produces: `sqlscan.Scan(sql string) (sqlscan.Usage, error)`; `type Usage struct { Writes, Reads, Functions []string }` — имена без схемы `public`, отсортированы, пустой список — `nil`; объект чужой схемы — `"<схема>.<имя>"`.

- [ ] **Step 1: Подключить парсер** (та же версия, что у sqlc в `backend/tools/go.mod:70`)

Run (из `backend/`): `go get github.com/wasilibs/go-pgquery@v0.0.0-20250409022910-10ac41983c07`
Expected: строка в `require` у `backend/go.mod`.

- [ ] **Step 2: Написать падающий тест**

`backend/internal/archtest/sqlscan/scan_test.go`:

```go
package sqlscan_test

import (
	"reflect"
	"testing"

	"wf/backend/internal/archtest/sqlscan"
)

func TestScan(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want sqlscan.Usage
	}{
		{
			name: "insert из CTE с join представления и вызовом функции",
			sql: `WITH x AS (SELECT id FROM teams)
				INSERT INTO matches (id) SELECT id FROM x JOIN city_settings cs ON true
				WHERE nearby_pitches(1, 2, 3) IS NOT NULL`,
			want: sqlscan.Usage{Writes: []string{"matches"}, Reads: []string{"city_settings", "teams"}, Functions: []string{"nearby_pitches"}},
		},
		{
			name: "update с from и delete в одном файле",
			sql:  `UPDATE users u SET status = 'active' FROM teams t WHERE t.id = u.id; DELETE FROM sessions WHERE id = $1`,
			want: sqlscan.Usage{Writes: []string{"sessions", "users"}, Reads: []string{"teams"}},
		},
		{
			name: "sqlc.arg не функция схемы, public снимается",
			sql:  `SELECT sqlc.arg(id)::uuid, p.* FROM public.pitches p`,
			want: sqlscan.Usage{Reads: []string{"pitches"}},
		},
		{
			name: "именованный параметр sqlc",
			sql: `-- name: GetFlag :one
SELECT key FROM feature_flags WHERE key = @key`,
			want: sqlscan.Usage{Reads: []string{"feature_flags"}},
		},
		{
			name: "@ в строке и в комментарии не ломает разбор",
			sql: `-- пишет a@b.ru
SELECT 1 FROM users WHERE email = 'a@b.ru'`,
			want: sqlscan.Usage{Reads: []string{"users"}},
		},
		{
			name: "truncate — запись",
			sql:  `TRUNCATE rate_limits`,
			want: sqlscan.Usage{Writes: []string{"rate_limits"}},
		},
		{
			name: "чужая схема квалифицируется",
			sql:  `SELECT relname FROM pg_catalog.pg_class`,
			want: sqlscan.Usage{Reads: []string{"pg_catalog.pg_class"}},
		},
		{
			name: "подзапрос в where",
			sql:  `SELECT 1 FROM teams WHERE city_id IN (SELECT id FROM cities)`,
			want: sqlscan.Usage{Reads: []string{"cities", "teams"}},
		},
		{
			name: "insert on conflict",
			sql:  `INSERT INTO outbox (aggregate_id) VALUES ($1) ON CONFLICT DO NOTHING`,
			want: sqlscan.Usage{Writes: []string{"outbox"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sqlscan.Scan(c.sql)
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestScanRejectsInvalidSQL(t *testing.T) {
	if _, err := sqlscan.Scan("SELEC 1"); err == nil {
		t.Fatal("ошибка синтаксиса не обнаружена")
	}
}
```

- [ ] **Step 3: Запустить — падает**

Run: `go test ./internal/archtest/sqlscan/ -count=1`
Expected: FAIL — `package wf/backend/internal/archtest/sqlscan` не содержит `Scan` (нет файлов пакета).

- [ ] **Step 4: Реализация**

`backend/internal/archtest/sqlscan/scan.go`:

```go
// Package sqlscan разбирает SQL модуля парсером PostgreSQL (pg_query в wasm, без cgo)
// и перечисляет объекты схемы, которые запрос пишет, читает и вызывает.
// Нужен стражу владения таблицами (internal/archtest, спека бэкенда §12.2).
package sqlscan

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	pgquery "github.com/wasilibs/go-pgquery"
)

// Usage — объекты схемы, которых касается SQL. Имена без схемы public, отсортированы;
// объект другой схемы — "<схема>.<имя>".
type Usage struct {
	Writes    []string // INSERT, UPDATE, DELETE, MERGE, COPY, TRUNCATE
	Reads     []string // FROM, JOIN, подзапросы — кроме имён CTE
	Functions []string // вызовы функций, кроме sqlc.*
}

// sqlc разрешает именованные параметры @name — для грамматики PostgreSQL это не SQL.
// Замена внутри строк и комментариев безвредна: разбор не ломается, имена таблиц там не ищутся.
var namedParam = regexp.MustCompile(`@[A-Za-z_][A-Za-z0-9_]*`)

// Операторы, у которых поле relation — цель записи.
var writeStmts = map[string]bool{
	"InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true, "MergeStmt": true, "CopyStmt": true,
}

func Scan(sql string) (Usage, error) {
	js, err := pgquery.ParseToJSON(namedParam.ReplaceAllString(sql, "$$1"))
	if err != nil {
		return Usage{}, fmt.Errorf("sqlscan: %w", err)
	}
	var tree any
	if err := json.Unmarshal([]byte(js), &tree); err != nil {
		return Usage{}, fmt.Errorf("sqlscan: дерево разбора: %w", err)
	}
	w := &walker{
		ctes:   map[string]bool{},
		writes: map[string]bool{},
		reads:  map[string]bool{},
		funcs:  map[string]bool{},
	}
	w.collectCTEs(tree)
	w.walk(tree, "")
	return Usage{Writes: sorted(w.writes), Reads: sorted(w.reads), Functions: sorted(w.funcs)}, nil
}

type walker struct {
	ctes, writes, reads, funcs map[string]bool
}

func (w *walker) collectCTEs(n any) {
	switch v := n.(type) {
	case map[string]any:
		if cte, ok := v["CommonTableExpr"].(map[string]any); ok {
			if name, ok := cte["ctename"].(string); ok {
				w.ctes[name] = true
			}
		}
		for _, c := range v {
			w.collectCTEs(c)
		}
	case []any:
		for _, c := range v {
			w.collectCTEs(c)
		}
	}
}

// walk обходит дерево; stmt — ближайший охватывающий узел-оператор (ключ вида InsertStmt).
func (w *walker) walk(n any, stmt string) {
	switch v := n.(type) {
	case map[string]any:
		for k, child := range v {
			switch {
			case k == "RangeVar":
				w.add(child, w.reads)
			case k == "relation" && writeStmts[stmt]:
				w.add(child, w.writes)
			case k == "relations" && stmt == "TruncateStmt":
				for _, item := range asList(child) {
					if m, ok := item.(map[string]any); ok {
						w.add(m["RangeVar"], w.writes)
					}
				}
				continue // иначе те же RangeVar засчитаются ещё и чтением
			case k == "FuncCall":
				w.addFunc(child)
			}
			next := stmt
			if isStmtNode(k) {
				next = k
			}
			w.walk(child, next)
		}
	case []any:
		for _, c := range v {
			w.walk(c, stmt)
		}
	}
}

// isStmtNode — узел-оператор (InsertStmt), а не поле (selectStmt).
func isStmtNode(k string) bool {
	return len(k) > 4 && k[0] >= 'A' && k[0] <= 'Z' && strings.HasSuffix(k, "Stmt")
}

func (w *walker) add(rel any, set map[string]bool) {
	m, ok := rel.(map[string]any)
	if !ok {
		return
	}
	name, _ := m["relname"].(string)
	schema, _ := m["schemaname"].(string)
	switch {
	case name == "":
		return
	case schema == "" && w.ctes[name]:
		return
	case schema != "" && schema != "public":
		name = schema + "." + name
	}
	set[name] = true
}

func (w *walker) addFunc(fc any) {
	m, ok := fc.(map[string]any)
	if !ok {
		return
	}
	var parts []string
	for _, p := range asList(m["funcname"]) {
		if s, ok := p.(map[string]any)["String"].(map[string]any); ok {
			if sval, ok := s["sval"].(string); ok {
				parts = append(parts, sval)
			}
		}
	}
	switch {
	case len(parts) == 0 || parts[0] == "sqlc":
		return
	case len(parts) == 2 && parts[0] == "public":
		parts = parts[1:]
	}
	w.funcs[strings.Join(parts, ".")] = true
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func sorted(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(set))
}
```

- [ ] **Step 5: Запустить — проходит**

Run: `go test ./internal/archtest/sqlscan/ -count=1`
Expected: PASS. Если упал только кейс `truncate` — распечатать `pgquery.ParseToJSON("TRUNCATE rate_limits")` и поправить ветку `relations` под фактическую форму узла (ожидается `{"TruncateStmt":{"relations":[{"RangeVar":{...}}]}}`).

- [ ] **Step 6: Commit**

```bash
git add backend/go.mod backend/go.sum backend/internal/archtest/sqlscan
git commit -m "archtest: разбор SQL модуля — запись, чтение, вызовы функций" -- backend/go.mod backend/go.sum backend/internal/archtest/sqlscan
```

---

### Task 2: Слои модулей и страж импортов

**Files:**
- Create: `backend/internal/archtest/modules.go`
- Test: `backend/internal/archtest/modules_test.go`

**Interfaces:**
- Consumes: —
- Produces (пакет `archtest`, используется Task 3):
  - `var Layers map[string]int` — модуль → слой (спека §4.3);
  - `var IntxPairs map[[2]string]bool` — `{импортирующий, владелец intx}` (спека §4.4);
  - `func importViolation(from, to string) string` — пусто, если импорт разрешён;
  - `func isKnownOwner(name string) bool` — модуль из `Layers` или `"platform"`.

- [ ] **Step 1: Написать падающий тест**

`backend/internal/archtest/modules_test.go`:

```go
package archtest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const p = "wf/backend/internal/"

// Подсадка багов: каждое нарушение из спеки §4.3–4.4 должно ловиться.
func TestImportViolation(t *testing.T) {
	allowed := [][2]string{
		{p + "teams/internal/app", p + "identity"},
		{p + "teams/internal/app", p + "identity/intx"},
		{p + "results/internal/app", p + "matches/intx"},
		{p + "moderation/internal/app", p + "identity/intx"},
		{p + "matches/internal/store", p + "platform/db"},
		{p + "teams/httpapi", p + "httpapi/public/oapi"},
		{p + "teams/admin", p + "httpapi/admin/oapi"},
		{p + "httpapi/public", p + "teams/httpapi"},
		{p + "platform/httpx", p + "platform/logx"},
		{"wf/backend/cmd/api", p + "teams/internal/app"},
		{p + "teams", "github.com/jackc/pgx/v5"},
		{p + "stats/subscribers", p + "results"},
	}
	for _, c := range allowed {
		if v := importViolation(c[0], c[1]); v != "" {
			t.Errorf("%s → %s должен быть разрешён, а: %s", c[0], c[1], v)
		}
	}
	forbidden := [][2]string{
		{p + "geo", p + "identity"},                                // вверх по слоям
		{p + "teams/internal/app", p + "matches"},                  // вверх по слоям
		{p + "stats", p + "notify"},                                // верхний слой друг друга не импортирует
		{p + "teams/internal/app", p + "identity/internal/app"},    // не корневой пакет
		{p + "teams/internal/app", p + "identity/httpapi"},         // не корневой пакет
		{p + "matches/internal/app", p + "identity/intx"},          // пары нет в §4.4
		{p + "platform/db", p + "teams"},                           // платформа не зависит от модулей
		{p + "teams/internal/app", p + "httpapi/public/oapi"},      // oapi — только из своего httpapi/
		{p + "teams/admin", p + "httpapi/public/oapi"},             // admin/ — только admin/oapi
		{p + "teams/httpapi", p + "httpapi/public"},                // сборка сервера
		{p + "teams", p + "archtest"},
	}
	for _, c := range forbidden {
		if v := importViolation(c[0], c[1]); v == "" {
			t.Errorf("%s → %s должен быть запрещён", c[0], c[1])
		}
	}
}

func TestInternalDirsAreKnown(t *testing.T) {
	entries, err := os.ReadDir("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !isKnownOwner(e.Name()) && !infra[e.Name()] {
			t.Errorf("internal/%s: неизвестный модуль — добавь его в archtest.Layers (спека §4.3)", e.Name())
		}
	}
}

// Главный страж: прямые импорты всех пакетов модуля wf/backend, включая тесты.
func TestModuleImportsRespectBoundaries(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-f",
		`{{.ImportPath}}{{range .Imports}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}`,
		"./...")
	cmd.Dir = "../.." // корень модуля backend
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("go list: %v\n%s", err, ee.Stderr)
		}
		t.Fatal(err)
	}
	seen := false
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		from := fields[0]
		if from == p+"platform/httpx" {
			seen = true
		}
		for _, to := range fields[1:] {
			if v := importViolation(from, to); v != "" {
				t.Errorf("%s → %s: %s", from, to, v)
			}
		}
	}
	if !seen {
		t.Fatal("go list не вернул internal/platform/httpx — страж проверяет вхолостую")
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/archtest/ -run 'TestImportViolation|TestInternalDirsAreKnown|TestModuleImports' -count=1`
Expected: FAIL — `undefined: importViolation`, `undefined: isKnownOwner`, `undefined: infra`.

- [ ] **Step 3: Реализация**

`backend/internal/archtest/modules.go`:

```go
package archtest

import (
	"fmt"
	"strings"
)

// Layers — модули бэкенда и их слой (спека бэкенда §4.3). Модуль импортирует только
// модули с меньшим слоем; верхний слой (9) друг друга не импортирует. Новый модуль —
// новая строка здесь, иначе TestInternalDirsAreKnown упадёт.
var Layers = map[string]int{
	"geo":          0,
	"identity":     1,
	"media":        2,
	"economy":      3,
	"players":      4,
	"pitches":      5,
	"teams":        6,
	"matches":      7,
	"results":      8,
	"stats":        9,
	"reputation":   9,
	"competitions": 9,
	"predictions":  9,
	"chat":         9,
	"moderation":   9,
	"notify":       9,
	"ads":          9,
}

// IntxPairs — кто может импортировать подпакет intx другого модуля (спека §4.4):
// ключ — {импортирующий модуль, владелец intx}. Расширяется только правкой спеки.
var IntxPairs = map[[2]string]bool{
	{"teams", "identity"}:      true, // капитанский допуск, смена города
	{"moderation", "identity"}: true, // санкции
	{"matches", "economy"}:     true, // платная бронь позиции
	{"teams", "economy"}:       true, // командные предметы
	{"predictions", "economy"}: true, // ставка прогноза
	{"results", "matches"}:     true, // явка и исход матча из протокола
}

// infra — каталоги internal/, которые не модули.
var infra = map[string]bool{"archtest": true, "httpapi": true, "platform": true}

const internalPrefix = "wf/backend/internal/"

// isKnownOwner — имя модуля из Layers или platform (владелец общих таблиц).
func isKnownOwner(name string) bool {
	_, ok := Layers[name]
	return ok || name == "platform"
}

// splitInternal — первый сегмент после internal/ и остаток пути.
func splitInternal(pkg string) (top, rest string, ok bool) {
	r, ok := strings.CutPrefix(pkg, internalPrefix)
	if !ok {
		return "", "", false
	}
	top, rest, _ = strings.Cut(r, "/")
	return top, rest, true
}

// under — rest равен dir или лежит внутри него.
func under(rest, dir string) bool {
	return rest == dir || strings.HasPrefix(rest, dir+"/")
}

// importViolation — почему импорт from → to нарушает границы модулей; "" — не нарушает.
// cmd/* и внешние пакеты — точки сборки и зависимости, их не ограничиваем.
func importViolation(from, to string) string {
	fTop, fRest, ok := splitInternal(from)
	if !ok {
		return ""
	}
	tTop, tRest, ok := splitInternal(to)
	if !ok || fTop == tTop {
		return ""
	}
	_, fromModule := Layers[fTop]
	_, toModule := Layers[tTop]
	switch {
	case fTop == "platform" && toModule:
		return "платформа не зависит от модулей"
	case fromModule && tTop == "httpapi":
		if under(fRest, "httpapi") && to == internalPrefix+"httpapi/public/oapi" ||
			under(fRest, "admin") && to == internalPrefix+"httpapi/admin/oapi" {
			return ""
		}
		return "модуль не зависит от сборки HTTP-сервера; сгенерированные типы oapi — только из своих httpapi/ и admin/"
	case fromModule && tTop == "archtest":
		return "модуль не зависит от archtest"
	case fromModule && toModule:
		if tRest != "" && tRest != "intx" {
			return fmt.Sprintf("у модуля %s можно импортировать только корневой пакет и intx", tTop)
		}
		if Layers[tTop] >= Layers[fTop] {
			return fmt.Sprintf("модуль %s (слой %d) не может зависеть от %s (слой %d) — спека §4.3",
				fTop, Layers[fTop], tTop, Layers[tTop])
		}
		if tRest == "intx" && !IntxPairs[[2]string{fTop, tTop}] {
			return fmt.Sprintf("%s → %s/intx нет в списке транзакций между модулями (спека §4.4)", fTop, tTop)
		}
	}
	return ""
}
```

- [ ] **Step 4: Запустить — проходит**

Run: `go test ./internal/archtest/ -count=1`
Expected: PASS (в том числе старые `TestPublicBinariesDoNotDependOnAdmin`, `TestIsAdminPackage`).

- [ ] **Step 5: Проверить страж на реальном дереве подсадкой**

Создать `backend/internal/geo/geo.go` с содержимым

```go
package geo

import _ "wf/backend/internal/identity"
```

и `backend/internal/identity/identity.go` с `package identity`.
Run: `go test ./internal/archtest/ -run TestModuleImportsRespectBoundaries -count=1`
Expected: FAIL `wf/backend/internal/geo → wf/backend/internal/identity: модуль geo (слой 0) не может зависеть от identity (слой 1)`.
Удалить оба файла и каталоги: `rm -f backend/internal/geo/geo.go backend/internal/identity/identity.go && rmdir backend/internal/geo backend/internal/identity`. Повторный прогон — PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/archtest/modules.go backend/internal/archtest/modules_test.go
git commit -m "archtest: слои модулей, пары intx и страж импортов" -- backend/internal/archtest/modules.go backend/internal/archtest/modules_test.go
```

---

### Task 3: Владельцы объектов схемы и страж SQL модулей

**Files:**
- Create: `backend/internal/archtest/ownership.go`
- Test: `backend/internal/archtest/ownership_test.go`

**Interfaces:**
- Consumes: `sqlscan.Scan`, `sqlscan.Usage` (Task 1); `Layers`, `isKnownOwner` (Task 2); `dbtest.NewPool(t) *pgxpool.Pool` (существует).
- Produces: `type Kind int` (`Table`, `View`, `Function`); `type Object struct { Kind Kind; Owner string; Exported bool }`; `var Schema map[string]Object`; `func ownerOf(name string) (Object, bool)`; `func ownershipViolations(module string, u sqlscan.Usage) []string`. Планы 2/3 и спеки модулей добавляют сюда новые таблицы.

- [ ] **Step 1: Написать падающий тест**

`backend/internal/archtest/ownership_test.go`:

```go
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
		{"matches", sqlscan.Usage{Reads: []string{"city_settings"}, Functions: []string{"nearby_pitches", "age_years", "count"}}},
		{"platform", sqlscan.Usage{Writes: []string{"river_job"}}},
		{"matches", sqlscan.Usage{Functions: []string{"matches_set_slot"}}},
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
		{"teams", sqlscan.Usage{Writes: []string{"city_settings"}}},          // запись в представление
		{"teams", sqlscan.Usage{Reads: []string{"no_such_table"}}},           // объект без владельца
		{"teams", sqlscan.Usage{Writes: []string{"no_such_table"}}},          // объект без владельца
		{"teams", sqlscan.Usage{Functions: []string{"matches_set_slot"}}},    // не экспортированная функция
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
		if o.Kind == View && o.Exported && !legacyExported[name] && !strings.HasPrefix(name, o.Owner+"_read_") {
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
	files, err := filepath.Glob("../*/queries/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	platformFiles, err := filepath.Glob("../platform/*/queries/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, platformFiles...)
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
		src, err := os.ReadFile(f)
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
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./internal/archtest/ -run 'Ownership|SchemaOwners|EverySchemaObject|ModuleQueries' -count=1`
Expected: FAIL — `undefined: ownershipViolations`, `undefined: Schema`, `undefined: ownerOf`, `undefined: legacyExported`.

- [ ] **Step 3: Реализация**

`backend/internal/archtest/ownership.go`:

```go
package archtest

import (
	"fmt"
	"strings"

	"wf/backend/internal/archtest/sqlscan"
)

// Kind — вид объекта схемы.
type Kind int

const (
	Table Kind = iota
	View
	Function
)

func (k Kind) String() string { return [...]string{"table", "view", "function"}[k] }

// Object — объект схемы и его модуль-владелец (спека бэкенда §4.1). Пишет объект только
// владелец; чужой модуль читает только экспортированные представления и вызывает только
// экспортированные функции (§4.2).
type Object struct {
	Kind     Kind
	Owner    string
	Exported bool
}

func table(owner string) Object { return Object{Kind: Table, Owner: owner} }

// Schema — владельцы всех объектов схемы public, кроме объектов расширений (PostGIS и др.)
// и служебных объектов River (ownerByPrefix). Новая таблица без записи здесь роняет
// TestEverySchemaObjectHasOwner, удалённая — тоже.
var Schema = map[string]Object{
	// platform
	"audit_log":        table("platform"),
	"outbox":           table("platform"),
	"idempotency_keys": table("platform"),
	"feature_flags":    table("platform"),
	"goose_db_version": table("platform"),

	"normalize_text":           {Function, "platform", true},
	"immutable_unaccent":       {Function, "platform", true},
	"age_years":                {Function, "platform", true},
	"set_updated_at":           {Function, "platform", false},
	"audit_log_is_append_only": {Function, "platform", false},

	// geo
	"countries":     table("geo"),
	"regions":       table("geo"),
	"cities":        table("geo"),
	"districts":     table("geo"),
	"city_settings": {View, "geo", true},

	// identity
	"users":                 table("identity"),
	"sessions":              table("identity"),
	"role_assignments":      table("identity"),
	"nickname_reservations": table("identity"),
	"nickname_changes":      table("identity"),
	"reserved_nicknames":    table("identity"),

	// pitches
	"pitches":        table("pitches"),
	"nearby_pitches": {Function, "pitches", true},

	// teams
	"teams":                table("teams"),
	"team_slug_history":    table("teams"),
	"team_members":         table("teams"),
	"team_join_requests":   table("teams"),
	"captaincy_transfers":  table("teams"),
	"captain_applications": table("teams"),

	// matches
	"matches":                           table("matches"),
	"match_slots":                       table("matches"),
	"match_participants":                table("matches"),
	"match_comments":                    table("matches"),
	"challenges":                        table("matches"),
	"challenge_responses":               table("matches"),
	"position_reservations":             table("matches"),
	"matches_set_slot":                  {Function, "matches", false},
	"position_reservations_revoke_limit": {Function, "matches", false},

	// results
	"match_reports": table("results"),
	"match_events":  table("results"),
	"match_votes":   table("results"),
	"match_ratings": table("results"),

	// stats
	"player_stats_daily": table("stats"),
	"team_stats_daily":   table("stats"),
	"team_ratings":       table("stats"),
	"team_stat_resets":   table("stats"),

	// reputation
	"reliability_snapshots":      table("reputation"),
	"reliability_reset_requests": table("reputation"),
	"payment_notes":              table("reputation"),
	"payment_flags":              table("reputation"),
	"payment_flag_disputes":      table("reputation"),

	// economy
	"wallets":           table("economy"),
	"ledger_entries":    table("economy"),
	"subscriptions":     table("economy"),
	"catalog_items":     table("economy"),
	"user_inventory":    table("economy"),
	"team_inventory":    table("economy"),
	"achievements":      table("economy"),
	"user_achievements": table("economy"),
	"lootbox_types":     table("economy"),
	"lootbox_grants":    table("economy"),
	"quests":            table("economy"),
	"user_quests":       table("economy"),
	"referrals":         table("economy"),

	// competitions, predictions
	"competitions":      table("competitions"),
	"competition_teams": table("competitions"),
	"predictions":       table("predictions"),

	// moderation
	"abuse_reports": table("moderation"),
	"sanctions":     table("moderation"),
	"anomaly_flags": table("moderation"),

	// notify
	"notifications":            table("notify"),
	"notification_preferences": table("notify"),
	"notification_settings":    table("notify"),
	"push_tokens":              table("notify"),

	// ads
	"ad_placements": table("ads"),
	"ad_dismissals": table("ads"),
}

// ownerByPrefix — объекты, которые создаёт не наш код: миграции River (набор таблиц
// и функций меняется от версии к версии).
var ownerByPrefix = map[string]string{"river_": "platform"}

// legacyExported — экспортированные представления, созданные до правила
// <модуль>_read_*. Новые сюда не добавлять.
var legacyExported = map[string]bool{"city_settings": true}

func ownerOf(name string) (Object, bool) {
	if o, ok := Schema[name]; ok {
		return o, true
	}
	for prefix, owner := range ownerByPrefix {
		if strings.HasPrefix(name, prefix) {
			return Object{Kind: Table, Owner: owner}, true
		}
	}
	return Object{}, false
}

// ownershipViolations — нарушения владения в SQL модуля module (спека §4.1–4.2).
func ownershipViolations(module string, u sqlscan.Usage) []string {
	var out []string
	for _, name := range u.Writes {
		o, ok := ownerOf(name)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("пишет %s — объекта нет в archtest.Schema", name))
		case o.Kind != Table:
			out = append(out, fmt.Sprintf("пишет в %s — это не таблица", name))
		case o.Owner != module:
			out = append(out, fmt.Sprintf("пишет чужую таблицу %s (владелец %s)", name, o.Owner))
		}
	}
	for _, name := range u.Reads {
		o, ok := ownerOf(name)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("читает %s — объекта нет в archtest.Schema", name))
		case o.Owner == module, o.Kind == View && o.Exported:
		default:
			out = append(out, fmt.Sprintf("читает %s модуля %s — чужое читается только из экспортированного представления (спека §4.2)", name, o.Owner))
		}
	}
	for _, name := range u.Functions {
		o, ok := Schema[name]
		if !ok || o.Kind != Function {
			continue // встроенные функции, функции расширений и River
		}
		if o.Owner != module && !o.Exported {
			out = append(out, fmt.Sprintf("вызывает %s модуля %s — функция не экспортирована", name, o.Owner))
		}
	}
	return out
}
```

- [ ] **Step 4: Запустить — проходит**

Run: `./task infra:up` (если Postgres не запущен), затем `go test ./internal/archtest/ -count=1`
Expected: PASS. Если `TestEverySchemaObjectHasOwner` назвал объект без владельца — это объект, которого не было в снимке схемы 01.10.2026: определить владельца по спеке §4.1 и добавить строку; не ослаблять запрос.

- [ ] **Step 5: Проверить страж на реальном SQL подсадкой**

Временно дописать в `backend/internal/platform/flags/queries/flags.sql`:

```sql
-- name: Planted :exec
UPDATE teams SET name = 'x' WHERE id = $1;
```

Run: `go test ./internal/archtest/ -run TestModuleQueriesRespectOwnership -count=1`
Expected: FAIL `…flags.sql (модуль platform): пишет чужую таблицу teams (владелец teams)`.
Удалить добавленные строки (файл снова совпадает с закоммиченным: `git diff --exit-code -- backend/internal/platform/flags/queries/flags.sql`). Повторный прогон — PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/archtest/ownership.go backend/internal/archtest/ownership_test.go
git commit -m "archtest: владельцы таблиц, представлений и функций; страж SQL модулей" -- backend/internal/archtest/ownership.go backend/internal/archtest/ownership_test.go
```

---

## Фаза B. Контракт

### Task 4: Контракт по модулям и сборка бандлов

**Files:**
- Create: `contracts/openapi/public/root.yaml`, `contracts/openapi/public/platform/paths.yaml`, `contracts/openapi/public/platform/schemas.yaml`
- Create: `contracts/openapi/admin/root.yaml`, `contracts/openapi/admin/platform/paths.yaml`, `contracts/openapi/admin/platform/schemas.yaml`
- Create: `contracts/scripts/bundle.mjs`
- Test: `contracts/test/bundle.test.mjs`
- Modify: `contracts/package.json`, `pnpm-lock.yaml`, `contracts/openapi/public.yaml`, `contracts/openapi/admin.yaml` (становятся бандлами), `backend/Taskfile.yml`, `.prettierignore`

**Interfaces:**
- Consumes: —
- Produces: `pnpm --filter @wf/contracts bundle` собирает `openapi/{public,admin}.yaml` из `openapi/{public,admin}/root.yaml`; `./task backend:gen` начинается со сборки бандлов. Раскладка (спека §8.1): операции модуля — `<модуль>/paths.yaml` (ключ — имя элемента пути), схемы — `<модуль>/schemas.yaml`, каждая схема регистрируется в `root.yaml` → `components.schemas` (имя в бандле задаёт `root.yaml`). Тег операции = имя модуля-реализатора; операции платформы — тег `platform`.

- [ ] **Step 1: Написать падающий тест**

`contracts/test/bundle.test.mjs`:

```js
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';

const load = async (path) =>
  parse(await readFile(new URL(`../openapi/${path}`, import.meta.url), 'utf8'));

for (const name of ['public', 'admin']) {
  test(`${name}.yaml — бандл, собранный из ${name}/root.yaml`, async () => {
    const [bundle, root] = await Promise.all([load(`${name}.yaml`), load(`${name}/root.yaml`)]);
    assert.equal(bundle.info.title, root.info.title);
    assert.deepEqual(Object.keys(bundle.paths).sort(), Object.keys(root.paths).sort());
    assert.deepEqual(bundle.paths['/v1/health'].get.tags, ['platform']);
    const refs = JSON.stringify(bundle).match(/"\$ref":"[^"]+"/g) ?? [];
    assert.deepEqual(
      refs.filter((r) => !r.startsWith('"$ref":"#/')),
      [],
      'в бандле остались внешние $ref',
    );
  });
}
```

- [ ] **Step 2: Запустить — падает**

Run: `pnpm --filter @wf/contracts test`
Expected: FAIL — `ENOENT … openapi/public/root.yaml`.

- [ ] **Step 3: Подключить redocly**

Run (из корня): `pnpm --filter @wf/contracts add -D @redocly/cli@2.57.0`
Expected: в `contracts/package.json` → `devDependencies` появился `"@redocly/cli": "2.57.0"`, обновлён `pnpm-lock.yaml`. Если pnpm отказал по `minimumReleaseAge` (версия опубликована 2026-09-30 12:47 UTC) — дождаться суток, исключение не добавлять.

- [ ] **Step 4: Исходники публичного контракта**

`contracts/openapi/public/root.yaml`:

```yaml
# Публичный API приложений и веба — исходник контракта (спека бэкенда §8.1).
# Бандл ../public.yaml собирает `pnpm --filter @wf/contracts bundle` (redocly); он коммитится
# и руками не правится. Операции модуля — <модуль>/paths.yaml, тег = имя модуля-реализатора;
# схемы — <модуль>/schemas.yaml, каждая регистрируется ниже в components.schemas.
# Схема Problem продублирована в admin/root.yaml намеренно (контракты версионируются
# независимо); тест в contracts/ падает, если копии разошлись. Версии — теги contracts-vX.Y.Z.
openapi: 3.0.3
info:
  title: WF public API
  version: 0.1.0
paths:
  /v1/health:
    $ref: "./platform/paths.yaml#/health"
components:
  responses:
    Problem:
      description: Ошибка в формате RFC 9457
      content:
        application/problem+json:
          schema:
            $ref: "#/components/schemas/Problem"
  schemas:
    Problem:
      type: object
      required: [type, title, status, code]
      properties:
        type:
          type: string
        title:
          type: string
        status:
          type: integer
        code:
          type: string
          description: Стабильный машинный код ошибки; клиенты переводят текст по нему
        detail:
          type: string
        request_id:
          type: string
    Health:
      $ref: "./platform/schemas.yaml#/Health"
```

`contracts/openapi/public/platform/paths.yaml`:

```yaml
# Операции платформы, не модуля: их реализует internal/httpapi/public.Server напрямую.
health:
  get:
    operationId: getHealth
    tags: [platform]
    summary: Проверка живости сервиса
    responses:
      "200":
        description: Сервис жив
        content:
          application/json:
            schema:
              $ref: "../root.yaml#/components/schemas/Health"
      default:
        $ref: "../root.yaml#/components/responses/Problem"
```

`contracts/openapi/public/platform/schemas.yaml`:

```yaml
Health:
  type: object
  required: [status]
  additionalProperties: false
  properties:
    status:
      type: string
      enum: [ok]
```

- [ ] **Step 5: Исходники админского контракта**

`contracts/openapi/admin/root.yaml` — тот же YAML, что `public/root.yaml`, кроме шапки и `info.title: WF admin API`. Шапка:

```yaml
# API админки (admin-api) — исходник контракта (спека бэкенда §8.1). Потребитель — только
# apps/admin. Бандл ../admin.yaml собирает `pnpm --filter @wf/contracts bundle` (redocly);
# он коммитится и руками не правится. Операции модуля — <модуль>/paths.yaml, тег = имя
# модуля-реализатора (internal/<модуль>/admin); схемы — <модуль>/schemas.yaml, каждая
# регистрируется ниже в components.schemas.
# Схема Problem продублирована в public/root.yaml намеренно (контракты версионируются
# независимо); тест в contracts/ падает, если копии разошлись.
```

`contracts/openapi/admin/platform/paths.yaml` — как публичный, первая строка: `# Операции платформы, не модуля: их реализует internal/httpapi/admin.Server напрямую.`
`contracts/openapi/admin/platform/schemas.yaml` — идентичен публичному.

- [ ] **Step 6: Скрипт сборки**

`contracts/scripts/bundle.mjs`:

```js
// Собирает бандлы контрактов из исходников по модулям: openapi/<имя>/root.yaml → openapi/<имя>.yaml.
// Бандл коммитится и руками не правится; backend:gen:check в CI сверяет его с исходниками.
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const win = process.platform === 'win32';
const redocly = fileURLToPath(
  new URL(`../node_modules/.bin/redocly${win ? '.cmd' : ''}`, import.meta.url),
);

for (const name of ['public', 'admin']) {
  execFileSync(redocly, ['bundle', `openapi/${name}/root.yaml`, '-o', `openapi/${name}.yaml`], {
    cwd: root,
    stdio: 'inherit',
    // телеметрию CLI выключаем: метаданные контракта наружу не уходят
    env: { ...process.env, REDOCLY_TELEMETRY: 'off' },
    // .cmd на Windows запускается только через оболочку
    shell: win,
  });
}
```

В `contracts/package.json` → `scripts` добавить `"bundle": "node scripts/bundle.mjs"`.

- [ ] **Step 7: Бандлы — сгенерированное, prettier их не трогает**

В `.prettierignore` после строки `**/src/api/gen/**` добавить:

```
contracts/openapi/public.yaml
contracts/openapi/admin.yaml
```

- [ ] **Step 8: Сборка бандлов входит в кодогенерацию бэкенда**

`backend/Taskfile.yml`, задача `gen`:

```yaml
  gen:
    desc: Кодогенерация бэкенда (бандлы контрактов, sqlc, oapi-codegen)
    cmds:
      - pnpm --filter @wf/contracts bundle
      - '{{.GOTOOL}} sqlc generate'
      - '{{.GOTOOL}} oapi-codegen -config internal/httpapi/public/oapi-codegen.yaml ../contracts/openapi/public.yaml'
      - '{{.GOTOOL}} oapi-codegen -config internal/httpapi/admin/oapi-codegen.yaml ../contracts/openapi/admin.yaml'
```

- [ ] **Step 9: Собрать и запустить тесты — проходят**

Run: `./task backend:gen`, затем `pnpm --filter @wf/contracts test`
Expected: PASS, включая старые тесты `contracts.test.mjs` (Problem одинаков, TS-типы генерируются). Бандл отличается от старого `public.yaml` тегом `platform`, порядком ключей и кавычками; в `backend/internal/httpapi/*/api.gen.go` меняется только вшитая спека.

- [ ] **Step 10: Проверить переводы строк и Go**

Run: `git add contracts/openapi && git ls-files --eol contracts/openapi/public.yaml contracts/openapi/admin.yaml`
Expected: `i/lf` у обоих. Затем из `backend/`: `go test ./internal/httpapi/... -count=1` — PASS.

- [ ] **Step 11: Commit**

```bash
git add contracts/openapi contracts/scripts contracts/test/bundle.test.mjs contracts/package.json pnpm-lock.yaml backend/Taskfile.yml backend/internal/httpapi/public/api.gen.go backend/internal/httpapi/admin/api.gen.go .prettierignore
git commit -m "Контракты по модулям: исходники в openapi/{public,admin}/, бандлы собирает redocly" -- contracts/openapi contracts/scripts contracts/test/bundle.test.mjs contracts/package.json pnpm-lock.yaml backend/Taskfile.yml backend/internal/httpapi/public/api.gen.go backend/internal/httpapi/admin/api.gen.go .prettierignore
```

---

### Task 5: Сгенерированные типы в пакете `oapi` и страж «тег = модуль»

**Files:**
- Modify: `backend/internal/httpapi/public/oapi-codegen.yaml`, `backend/internal/httpapi/admin/oapi-codegen.yaml`
- Delete: `backend/internal/httpapi/public/api.gen.go`, `backend/internal/httpapi/admin/api.gen.go`
- Create (сгенерировано): `backend/internal/httpapi/public/oapi/api.gen.go`, `backend/internal/httpapi/admin/oapi/api.gen.go`
- Modify: `backend/internal/httpapi/{public,admin}/server.go`, `handler.go`, `handler_test.go`
- Create: `backend/internal/platform/testkit/apitest/tags.go`
- Test: `backend/internal/platform/testkit/apitest/tags_test.go`

**Interfaces:**
- Consumes: бандлы Task 4.
- Produces:
  - пакеты `wf/backend/internal/httpapi/public/oapi` и `wf/backend/internal/httpapi/admin/oapi` (package `oapi`): `StrictServerInterface`, `NewStrictHandlerWithOptions`, `HandlerFromMux`, `StrictHTTPServerOptions`, `GetSpec() (*openapi3.T, error)`, типы `GetHealth*`, константа `Ok`;
  - `public.Server`, `admin.Server` — сборка: встраивают хендлеры модулей, операции платформы реализуют сами;
  - `apitest.Operation{Method, Tag string}`; `apitest.Operations(spec *openapi3.T) ([]apitest.Operation, error)`; `apitest.Owners(server reflect.Type, moduleOf func(pkgPath string) string) map[string]string`; `apitest.ModuleOf(pkgPath string) string`; `apitest.TagViolations(ops []apitest.Operation, owners map[string]string) []string`.

- [ ] **Step 1: Написать падающий тест стража на синтетике**

`backend/internal/platform/testkit/apitest/tags_test.go`:

```go
package apitest_test

import (
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/testkit/apitest"
)

type fakeTeams struct{}

func (fakeTeams) GetTeam() {}

type fakeServer struct{ fakeTeams }

func (fakeServer) GetHealth() {}

func TestOwners(t *testing.T) {
	got := apitest.Owners(reflect.TypeOf(fakeServer{}), func(string) string { return "teams" })
	want := map[string]string{"GetTeam": "teams", "GetHealth": "platform"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTagViolations(t *testing.T) {
	owners := map[string]string{"GetTeam": "teams", "GetHealth": "platform"}
	ok := []apitest.Operation{{Method: "GetTeam", Tag: "teams"}, {Method: "GetHealth", Tag: "platform"}}
	if v := apitest.TagViolations(ok, owners); len(v) != 0 {
		t.Fatalf("лишние нарушения: %v", v)
	}
	// подсадка: тег не совпадает с реализатором; операция без реализации
	bad := []apitest.Operation{{Method: "GetTeam", Tag: "matches"}, {Method: "GetMissing", Tag: "teams"}}
	if v := apitest.TagViolations(bad, owners); len(v) != 2 {
		t.Fatalf("ожидалось 2 нарушения, есть %v", v)
	}
}

func TestModuleOf(t *testing.T) {
	cases := map[string]string{
		"wf/backend/internal/teams/httpapi": "teams",
		"wf/backend/internal/teams/admin":   "teams",
		"github.com/x/y":                    "",
	}
	for in, want := range cases {
		if got := apitest.ModuleOf(in); got != want {
			t.Errorf("ModuleOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOperationsNeedsExactlyOneTag(t *testing.T) {
	load := func(tags string) *openapi3.T {
		t.Helper()
		doc, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      tags: ` + tags + `
      responses: {"200": {description: ok}}
`))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	ops, err := apitest.Operations(load("[teams]"))
	if err != nil || !reflect.DeepEqual(ops, []apitest.Operation{{Method: "GetX", Tag: "teams"}}) {
		t.Fatalf("ops = %v, err = %v", ops, err)
	}
	if _, err := apitest.Operations(load("[teams, matches]")); err == nil {
		t.Fatal("два тега не отклонены")
	}
	if _, err := apitest.Operations(load("[]")); err == nil {
		t.Fatal("операция без тега не отклонена")
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run (из `backend/`): `go test ./internal/platform/testkit/apitest/ -count=1`
Expected: FAIL — `undefined: apitest.Owners` и др.

- [ ] **Step 3: Реализация стража**

`backend/internal/platform/testkit/apitest/tags.go`:

```go
package apitest

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Operation — операция контракта: имя метода strict-сервера и тег — модуль-реализатор.
type Operation struct{ Method, Tag string }

// Operations перечисляет операции спеки. Имя метода — operationId с заглавной буквы,
// как его называет oapi-codegen (operationId — lowerCamel, это проверяет contracts/).
func Operations(spec *openapi3.T) ([]Operation, error) {
	var out []Operation
	for path, item := range spec.Paths.Map() {
		for verb, op := range item.Operations() {
			if len(op.Tags) != 1 {
				return nil, fmt.Errorf("%s %s: нужен ровно один тег — имя модуля, есть %v", verb, path, op.Tags)
			}
			id := op.OperationID
			if id == "" {
				return nil, fmt.Errorf("%s %s: нет operationId", verb, path)
			}
			out = append(out, Operation{Method: strings.ToUpper(id[:1]) + id[1:], Tag: op.Tags[0]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out, nil
}

// Owners — какой модуль реализует каждый метод сервера: методы встроенных хендлеров
// принадлежат их модулю (moduleOf по пути пакета), объявленные на самом сервере — platform.
func Owners(server reflect.Type, moduleOf func(pkgPath string) string) map[string]string {
	owners := map[string]string{}
	for i := range server.NumField() {
		f := server.Field(i)
		if !f.Anonymous {
			continue
		}
		elem, ptr := f.Type, f.Type
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		} else {
			ptr = reflect.PointerTo(elem)
		}
		module := moduleOf(elem.PkgPath())
		for m := range ptr.NumMethod() {
			owners[ptr.Method(m).Name] = module
		}
	}
	ptr := reflect.PointerTo(server)
	for m := range ptr.NumMethod() {
		if name := ptr.Method(m).Name; owners[name] == "" {
			owners[name] = "platform"
		}
	}
	return owners
}

// ModuleOf — модуль по пути пакета хендлера wf/backend/internal/<модуль>/{httpapi,admin}.
func ModuleOf(pkgPath string) string {
	rest, ok := strings.CutPrefix(pkgPath, "wf/backend/internal/")
	if !ok {
		return ""
	}
	module, _, _ := strings.Cut(rest, "/")
	return module
}

// TagViolations — операции, которые реализует не тот модуль, что указан в теге.
func TagViolations(ops []Operation, owners map[string]string) []string {
	var out []string
	for _, op := range ops {
		owner, ok := owners[op.Method]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: сервер не реализует операцию", op.Method))
		case owner != op.Tag:
			out = append(out, fmt.Sprintf("%s: тег %q, а реализует модуль %q", op.Method, op.Tag, owner))
		}
	}
	return out
}
```

Run: `go test ./internal/platform/testkit/apitest/ -count=1` — PASS.

- [ ] **Step 4: Перенести генерацию в пакет `oapi`**

`backend/internal/httpapi/public/oapi-codegen.yaml`:

```yaml
# Сгенерированные типы и strict-интерфейс — отдельный пакет: хендлеры модулей
# (internal/<модуль>/httpapi) импортируют типы, а сборка сервера — хендлеры (спека §8.2).
package: oapi
output: internal/httpapi/public/oapi/api.gen.go
generate:
  models: true
  chi-server: true
  strict-server: true
  embedded-spec: true
```

`backend/internal/httpapi/admin/oapi-codegen.yaml` — то же, комментарий про `internal/<модуль>/admin`, `output: internal/httpapi/admin/oapi/api.gen.go`.

Run (из корня): `git rm -q backend/internal/httpapi/public/api.gen.go backend/internal/httpapi/admin/api.gen.go && mkdir -p backend/internal/httpapi/public/oapi backend/internal/httpapi/admin/oapi && ./task backend:gen`
Expected: созданы `oapi/api.gen.go` в обоих каталогах; пакеты `public` и `admin` пока не компилируются.

- [ ] **Step 5: Тест тегов в сборке сервера**

`backend/internal/httpapi/public/handler_test.go` целиком:

```go
package public_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/testkit/apitest"
)

func TestHealthMatchesContract(t *testing.T) {
	v := apitest.New(t, oapi.GetSpec)
	rec := v.Do(t, public.NewHandler(slog.New(slog.DiscardHandler)),
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// Страж спеки §12.4: операцию с тегом <модуль> реализует хендлер этого модуля.
func TestOperationsImplementedByTaggedModule(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	ops, err := apitest.Operations(spec)
	if err != nil {
		t.Fatal(err)
	}
	owners := apitest.Owners(reflect.TypeOf(public.Server{}), apitest.ModuleOf)
	for _, v := range apitest.TagViolations(ops, owners) {
		t.Error(v)
	}
}
```

`backend/internal/httpapi/admin/handler_test.go` — то же с `package admin_test`, импортами `wf/backend/internal/httpapi/admin` и `wf/backend/internal/httpapi/admin/oapi`, `admin.NewHandler`, `admin.Server{}`.

Run: `go test ./internal/httpapi/... -count=1`
Expected: FAIL — компиляция `server.go`: `undefined: StrictServerInterface`.

- [ ] **Step 6: Сборка сервера на `oapi`**

`backend/internal/httpapi/public/server.go`:

```go
package public

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Server собирает публичный API: встраивает хендлеры модулей (internal/<модуль>/httpapi),
// операции платформы (тег platform) реализует сам. Компилятор ловит и нереализованную
// операцию, и реализованную двумя модулями (неоднозначный метод) — спека §8.2.
type Server struct{}

var _ oapi.StrictServerInterface = Server{}

func (Server) GetHealth(context.Context, oapi.GetHealthRequestObject) (oapi.GetHealthResponseObject, error) {
	return oapi.GetHealth200JSONResponse{Status: oapi.Ok}, nil
}
```

`backend/internal/httpapi/public/handler.go`:

```go
package public

import (
	"log/slog"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает публичный API на общей HTTP-платформе.
func NewHandler(log *slog.Logger) http.Handler {
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	return oapi.HandlerFromMux(strict, httpx.NewRouter(log))
}
```

Админские `server.go` и `handler.go` — то же с `package admin`, импортом `wf/backend/internal/httpapi/admin/oapi`, комментариями «API админки» и «хендлеры модулей (internal/<модуль>/admin)».

- [ ] **Step 7: Запустить — проходит; подсадка**

Run: `go test ./internal/httpapi/... ./internal/archtest/ -count=1` — PASS.
Подсадка: в `contracts/openapi/public/platform/paths.yaml` временно поставить `tags: [teams]`, `./task backend:gen`, `go test ./internal/httpapi/public/ -run TestOperationsImplementedByTaggedModule -count=1` — FAIL `GetHealth: тег "teams", а реализует модуль "platform"`. Вернуть `tags: [platform]`, `./task backend:gen`, прогон — PASS; `git diff --stat -- contracts/openapi/public/platform/paths.yaml` пуст.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/httpapi backend/internal/platform/testkit/apitest/tags.go backend/internal/platform/testkit/apitest/tags_test.go
git commit -m "httpapi: сгенерированные типы в пакете oapi, страж «тег операции = модуль-реализатор»" -- backend/internal/httpapi backend/internal/platform/testkit/apitest/tags.go backend/internal/platform/testkit/apitest/tags_test.go
```

---

### Task 6: Конвенции контракта — аутентификация, Idempotency-Key, коды ошибок

**Files:**
- Modify: `contracts/openapi/public/root.yaml`, `contracts/openapi/public/platform/paths.yaml`, `contracts/openapi/admin/root.yaml`, `contracts/openapi/admin/platform/paths.yaml`
- Create: `contracts/src/conventions.mjs`
- Test: `contracts/test/conventions.test.mjs`
- Modify (сгенерировано): `contracts/openapi/{public,admin}.yaml`, `backend/internal/httpapi/{public,admin}/oapi/api.gen.go`, `apps/{web,admin}/src/api/gen/*.d.ts`

**Interfaces:**
- Consumes: бандлы Task 4.
- Produces:
  - в контрактах: `security` по умолчанию (`bearer` — публичный, `session` — cookie `wf_admin_session` в админке), `components.parameters.IdempotencyKey`, корневой `x-error-codes-common`, у каждой операции `x-error-codes`, у `Problem` поле `errors: [{field, code}]`;
  - `contracts/src/conventions.mjs`: `conventionViolations(doc) → string[]`, `errorCodes(doc) → string[]` (общие коды + коды операций, уникальные, отсортированы), `operations(doc) → {path, verb, op}[]`.
- Правило Idempotency-Key (спека §6.4): мутирующая операция (`post`, `put`, `patch`, `delete`) с непустой действующей `security` обязана ссылаться на `#/components/parameters/IdempotencyKey`; анонимная (`security: []`) — нет.

- [ ] **Step 1: Написать падающий тест**

`contracts/test/conventions.test.mjs`:

```js
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { conventionViolations, errorCodes } from '../src/conventions.mjs';

const load = async (name) =>
  parse(await readFile(new URL(`../openapi/${name}.yaml`, import.meta.url), 'utf8'));

for (const name of ['public', 'admin']) {
  test(`${name}.yaml соблюдает конвенции контракта`, async () => {
    assert.deepEqual(conventionViolations(await load(name)), []);
  });
}

test('Problem описывает ошибки полей', async () => {
  const errors = (await load('public')).components.schemas.Problem.properties.errors;
  assert.deepEqual(errors.items.required, ['field', 'code']);
});

const base = () => ({
  openapi: '3.0.3',
  security: [{ bearer: [] }],
  'x-error-codes-common': ['internal'],
  paths: {
    '/v1/teams': {
      post: {
        operationId: 'createTeam',
        tags: ['teams'],
        'x-error-codes': ['teams.name_taken'],
        parameters: [{ $ref: '#/components/parameters/IdempotencyKey' }],
        responses: {},
      },
    },
  },
  components: { schemas: { Team: { type: 'object' } } },
});

test('эталонный документ без нарушений', () => {
  assert.deepEqual(conventionViolations(base()), []);
});

// Подсадка багов: каждое нарушение конвенций ловится.
const planted = {
  'мутирующая операция с аутентификацией без Idempotency-Key': (d) => {
    d.paths['/v1/teams'].post.parameters = [];
  },
  'два тега': (d) => {
    d.paths['/v1/teams'].post.tags = ['teams', 'matches'];
  },
  'operationId в snake_case': (d) => {
    d.paths['/v1/teams'].post.operationId = 'create_team';
  },
  'нет x-error-codes': (d) => {
    delete d.paths['/v1/teams'].post['x-error-codes'];
  },
  'код ошибки не по формату': (d) => {
    d.paths['/v1/teams'].post['x-error-codes'] = ['Teams.NameTaken'];
  },
  'внешний $ref — контракт не собран': (d) => {
    d.components.schemas.Team = { $ref: './teams/schemas.yaml#/Team' };
  },
  'схема, переименованная redocly': (d) => {
    d.components.schemas['Team-2'] = { type: 'object' };
  },
  'нет x-error-codes-common': (d) => {
    delete d['x-error-codes-common'];
  },
};
for (const [name, plant] of Object.entries(planted)) {
  test(`ловит: ${name}`, () => {
    const d = base();
    plant(d);
    assert.notDeepEqual(conventionViolations(d), []);
  });
}

test('анонимная мутирующая операция обходится без Idempotency-Key', () => {
  const d = base();
  Object.assign(d.paths['/v1/teams'].post, { security: [], parameters: [] });
  assert.deepEqual(conventionViolations(d), []);
});

test('errorCodes — общие коды и коды операций без повторов', () => {
  const d = base();
  d.paths['/v1/x'] = { get: { 'x-error-codes': ['teams.name_taken', 'teams.not_found'] } };
  assert.deepEqual(errorCodes(d), ['internal', 'teams.name_taken', 'teams.not_found']);
});
```

- [ ] **Step 2: Запустить — падает**

Run: `pnpm --filter @wf/contracts test`
Expected: FAIL — `Cannot find module …/contracts/src/conventions.mjs`.

- [ ] **Step 3: Реализация проверок**

`contracts/src/conventions.mjs`:

```js
// Конвенции контрактов (спека бэкенда §6.4, §6.8, §8.1–8.3): проверяются на собранных бандлах.

const VERBS = ['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace'];
const MUTATING = new Set(['post', 'put', 'patch', 'delete']);
const OPERATION_ID = /^[a-z][A-Za-z0-9]*$/;
const ERROR_CODE = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$/;
const IDEMPOTENCY_REF = '#/components/parameters/IdempotencyKey';

export function operations(doc) {
  return Object.entries(doc.paths ?? {}).flatMap(([path, item]) =>
    Object.entries(item)
      .filter(([verb]) => VERBS.includes(verb))
      .map(([verb, op]) => ({ path, verb, op })),
  );
}

function refs(node, out = []) {
  if (Array.isArray(node)) node.forEach((n) => refs(n, out));
  else if (node && typeof node === 'object') {
    for (const [k, v] of Object.entries(node)) {
      if (k === '$ref' && typeof v === 'string') out.push(v);
      else refs(v, out);
    }
  }
  return out;
}

export function conventionViolations(doc) {
  const out = [];
  if (!Array.isArray(doc['x-error-codes-common'])) {
    out.push('нет x-error-codes-common — общих кодов ошибок платформы');
  }
  for (const { path, verb, op } of operations(doc)) {
    const where = `${verb.toUpperCase()} ${path}`;
    if (!OPERATION_ID.test(op.operationId ?? '')) {
      out.push(`${where}: operationId «${op.operationId}» — нужен lowerCamel`);
    }
    if (op.tags?.length !== 1) out.push(`${where}: нужен ровно один тег — имя модуля`);
    const codes = op['x-error-codes'];
    if (!Array.isArray(codes)) out.push(`${where}: нет x-error-codes`);
    else {
      for (const c of codes) {
        if (!ERROR_CODE.test(c)) out.push(`${where}: код ошибки «${c}» — нужен <модуль>.<ошибка>`);
      }
    }
    const security = op.security ?? doc.security ?? [];
    const hasKey = (op.parameters ?? []).some((p) => p.$ref === IDEMPOTENCY_REF);
    if (MUTATING.has(verb) && security.length > 0 && !hasKey) {
      out.push(`${where}: мутирующая операция с аутентификацией без Idempotency-Key`);
    }
  }
  for (const ref of refs(doc)) {
    if (!ref.startsWith('#/')) out.push(`внешний $ref ${ref} — контракт не собран`);
  }
  for (const name of Object.keys(doc.components?.schemas ?? {})) {
    if (/-\d+$/.test(name)) {
      out.push(`схема ${name}: redocly переименовал дубль — зарегистрируй схему в root.yaml`);
    }
  }
  return out;
}

export function errorCodes(doc) {
  const codes = [
    ...(doc['x-error-codes-common'] ?? []),
    ...operations(doc).flatMap(({ op }) => op['x-error-codes'] ?? []),
  ];
  return [...new Set(codes)].sort();
}
```

Run: `pnpm --filter @wf/contracts test`
Expected: синтетические тесты PASS; тесты реальных бандлов FAIL — `нет x-error-codes-common`, `GET /v1/health: нет x-error-codes`, `Problem описывает ошибки полей`.

- [ ] **Step 4: Привести исходники контрактов к конвенциям**

В `contracts/openapi/public/root.yaml` после блока `info` вставить:

```yaml
# Аутентификация по умолчанию; анонимная операция объявляет `security: []`.
security:
  - bearer: []
# Коды ошибок платформы — их может вернуть любая операция; у операций — свои x-error-codes.
x-error-codes-common:
  - internal
  - request.invalid
  - request.too_large
  - validation.failed
  - http.not_found
  - http.method_not_allowed
```

В `components` (перед `responses`) вставить:

```yaml
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
      bearerFormat: JWT
  parameters:
    IdempotencyKey:
      name: Idempotency-Key
      in: header
      required: true
      description: Ключ одного действия пользователя; повтор с тем же ключом не исполняется дважды
      schema:
        type: string
        minLength: 16
        maxLength: 128
```

В `components.schemas.Problem.properties` после `request_id` добавить:

```yaml
        errors:
          type: array
          description: Ошибки по полям — только у validation.failed
          items:
            type: object
            required: [field, code]
            additionalProperties: false
            properties:
              field:
                type: string
                description: "body.<путь через точку>, query.<имя>, path.<имя> или header.<имя>"
              code:
                type: string
                description: "Нарушенное правило схемы: required, minLength, maximum, enum, format…"
```

В `contracts/openapi/public/platform/paths.yaml` у `health.get` после `tags` добавить:

```yaml
    security: []
    x-error-codes: []
```

`contracts/openapi/admin/root.yaml` — те же вставки, кроме схемы безопасности:

```yaml
security:
  - session: []
```

```yaml
  securitySchemes:
    session:
      type: apiKey
      in: cookie
      name: wf_admin_session
```

(`parameters.IdempotencyKey`, `x-error-codes-common`, `Problem.errors` — идентичны публичному: тест одинаковости `Problem` в `contracts.test.mjs` это проверит.) В `contracts/openapi/admin/platform/paths.yaml` — `security: []` и `x-error-codes: []`.

- [ ] **Step 5: Пересобрать и запустить — проходит**

Run: `./task gen` (бандлы, Go, TS-типы веба и админки), затем `pnpm --filter @wf/contracts test` и из `backend/`: `go test ./internal/httpapi/... -count=1`
Expected: PASS везде.

- [ ] **Step 6: Commit**

```bash
git add contracts/openapi contracts/src/conventions.mjs contracts/test/conventions.test.mjs backend/internal/httpapi apps/web/src/api/gen apps/admin/src/api/gen
git commit -m "Контракты: аутентификация по умолчанию, Idempotency-Key, коды ошибок, errors в Problem, проверка конвенций" -- contracts/openapi contracts/src/conventions.mjs contracts/test/conventions.test.mjs backend/internal/httpapi apps/web/src/api/gen apps/admin/src/api/gen
```

---

### Task 7: Валидация запросов по контракту и лимит тела

**Files:**
- Modify: `backend/internal/platform/httpx/problem.go`, `backend/internal/platform/httpx/router.go`
- Create: `backend/internal/platform/httpx/validate.go`
- Test: `backend/internal/platform/httpx/validate_test.go`
- Modify: `backend/internal/platform/server/server.go`, `backend/internal/platform/server/server_test.go`
- Modify: `backend/internal/httpapi/{public,admin}/handler.go`, `handler_test.go`
- Modify: `backend/cmd/api/main.go`, `backend/cmd/admin-api/main.go`

**Interfaces:**
- Consumes: `oapi.GetSpec` (Task 5); `Problem.errors` в контракте (Task 6).
- Produces:
  - `httpx.FieldError{Field, Code string}`; `httpx.Problem.Errors []FieldError` (`json:"errors,omitempty"`); `httpx.WriteValidationProblem(w, r, errs []FieldError)` — `400 validation.failed`;
  - `httpx.MaxBodyBytes = 1 << 20`; `httpx.LimitBody(n int64) func(http.Handler) http.Handler`;
  - `httpx.ValidateRequests(spec *openapi3.T) (func(http.Handler) http.Handler, error)` — `400 validation.failed` с полями, `400 request.invalid` (тело не разбирается, неверный Content-Type), `413 request.too_large`; маршрут не из контракта пропускает;
  - `httpx.NewRouter(log *slog.Logger, mws ...func(http.Handler) http.Handler) chi.Router` — middleware бинарника после общих;
  - `public.NewHandler(log) (http.Handler, error)`, `admin.NewHandler(log) (http.Handler, error)`;
  - `server.RunHTTP(ctx, name, c, logOut, build func(*slog.Logger, *pgxpool.Pool) (http.Handler, error)) error`.

- [ ] **Step 1: Написать падающий тест**

`backend/internal/platform/httpx/validate_test.go`:

```go
package httpx_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"wf/backend/internal/platform/httpx"
)

const thingsSpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      parameters:
        - name: Idempotency-Key
          in: header
          required: true
          schema: {type: string, minLength: 16}
        - name: limit
          in: query
          schema: {type: integer, maximum: 100}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name, kind]
              properties:
                name: {type: string, minLength: 2}
                kind: {type: string, enum: [a, b]}
      responses:
        "204": {description: ok}
`

// handler — валидация поверх хендлера, который запоминает тело и отвечает 204.
func handler(t *testing.T) (http.Handler, *string) {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(thingsSpec))
	if err != nil {
		t.Fatal(err)
	}
	validate, err := httpx.ValidateRequests(spec)
	if err != nil {
		t.Fatal(err)
	}
	seen := new(string)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*seen = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	return httpx.LimitBody(64)(validate(next)), seen
}

func TestValidateRequests(t *testing.T) {
	const okBody = `{"name":"ab","kind":"a"}`
	cases := []struct {
		name, body, query, contentType string
		noKey                          bool
		status                         int
		code                           string
		errs                           []httpx.FieldError
	}{
		{name: "валидный запрос доходит до хендлера", body: okBody, status: 204},
		{name: "короткое имя", body: `{"name":"a","kind":"a"}`, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.name", Code: "minLength"}}},
		{name: "нет обязательного свойства", body: `{"name":"ab"}`, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.kind", Code: "required"}}},
		{name: "два нарушения сразу — по порядку полей", body: `{"name":"a","kind":"z"}`, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "body.kind", Code: "enum"}, {Field: "body.name", Code: "minLength"}}},
		{name: "нет обязательного заголовка", body: okBody, noKey: true, status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "header.Idempotency-Key", Code: "required"}}},
		{name: "query больше максимума", body: okBody, query: "limit=500", status: 400, code: "validation.failed",
			errs: []httpx.FieldError{{Field: "query.limit", Code: "maximum"}}},
		{name: "битый JSON", body: `{`, status: 400, code: "request.invalid"},
		{name: "неверный Content-Type", body: okBody, contentType: "text/plain", status: 400, code: "request.invalid"},
		{name: "тело больше лимита", body: `{"name":"` + strings.Repeat("a", 80) + `","kind":"a"}`, status: 413, code: "request.too_large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, seen := handler(t)
			url := "/things"
			if c.query != "" {
				url += "?" + c.query
			}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(c.body))
			ct := c.contentType
			if ct == "" {
				ct = "application/json"
			}
			req.Header.Set("Content-Type", ct)
			if !c.noKey {
				req.Header.Set("Idempotency-Key", strings.Repeat("k", 16)) // без энтропии — gitleaks не примет за секрет
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, c.status, rec.Body.String())
			}
			if c.status == http.StatusNoContent {
				if *seen != c.body {
					t.Fatalf("хендлер получил тело %q, want %q", *seen, c.body)
				}
				return
			}
			var p httpx.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatalf("тело не JSON: %s", rec.Body.String())
			}
			if p.Code != c.code || !reflect.DeepEqual(p.Errors, c.errs) {
				t.Fatalf("code = %q errors = %+v, want %q %+v", p.Code, p.Errors, c.code, c.errs)
			}
			if p.Detail != "" {
				t.Fatalf("detail раскрывает внутренности валидатора: %q", p.Detail)
			}
		})
	}
}

func TestValidateRequestsPassesUnknownRoute(t *testing.T) {
	h, _ := handler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("маршрут не из контракта должен идти дальше, status = %d", rec.Code)
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run (из `backend/`): `go test ./internal/platform/httpx/ -count=1`
Expected: FAIL — `undefined: httpx.ValidateRequests`, `undefined: httpx.FieldError`, `undefined: httpx.LimitBody`.

- [ ] **Step 3: Problem с ошибками полей**

`backend/internal/platform/httpx/problem.go` — заменить тип `Problem` и функцию `WriteProblem`, остальное без изменений:

```go
// FieldError — нарушение схемы в одном поле запроса (у validation.failed).
// Field — body.<путь через точку>, query.<имя>, path.<имя>, header.<имя>;
// Code — правило схемы: required, minLength, maximum, enum, format, type, pattern…
type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// Problem — ответ об ошибке по RFC 9457. Клиенты переводят текст по Code;
// Title — техническая сводка (http.StatusText), не для показа пользователю.
type Problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Code      string       `json:"code"`
	Detail    string       `json:"detail,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`
}

func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	writeProblem(w, r, Problem{Status: status, Code: code, Detail: detail})
}

// WriteValidationProblem — 400 validation.failed со списком нарушенных полей.
func WriteValidationProblem(w http.ResponseWriter, r *http.Request, errs []FieldError) {
	writeProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "validation.failed", Errors: errs})
}

func writeProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	p.Type = "about:blank"
	p.Title = http.StatusText(p.Status)
	p.RequestID = middleware.GetReqID(r.Context())
	w.Header().Set("Content-Type", ContentTypeProblem)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
```

- [ ] **Step 4: Валидация и лимит тела**

`backend/internal/platform/httpx/validate.go`:

```go
package httpx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// MaxBodyBytes — лимит тела запроса по умолчанию: JSON API больших тел не ждёт (спека §6.1).
const MaxBodyBytes = 1 << 20

// LimitBody ограничивает тело запроса n байтами. Чтение сверх лимита — *http.MaxBytesError;
// ValidateRequests отвечает на неё 413 request.too_large.
func LimitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// ValidateRequests проверяет запрос по контракту до strict-хендлера: strict-сервер
// oapi-codegen схему сам не валидирует (minLength, enum, format, обязательные заголовки).
// Маршрут не из контракта пропускается дальше — его ответит роутер (404/405).
// Аутентификация здесь не проверяется — это отдельный слой конвейера (спека §6.1).
func ValidateRequests(spec *openapi3.T) (func(http.Handler) http.Handler, error) {
	spec.Servers = nil // иначе FindRoute сверяет хост и префикс из servers
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("httpx: роутер контракта: %w", err)
	}
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, MultiError: true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, params, err := router.FindRoute(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			var body []byte
			if r.Body != nil {
				if body, err = io.ReadAll(r.Body); err != nil {
					if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
						WriteProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "")
						return
					}
					WriteProblem(w, r, http.StatusBadRequest, "request.invalid", "")
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: opts}
			verr := openapi3filter.ValidateRequest(r.Context(), in)
			if r.Body != nil {
				r.Body = io.NopCloser(bytes.NewReader(body)) // валидатор прочитал тело — хендлеру нужно целое
			}
			if verr != nil {
				if fields, ok := fieldErrors(verr); ok {
					WriteValidationProblem(w, r, fields)
					return
				}
				// текст ошибки kin-openapi клиенту не отдаём: он раскрывает устройство валидатора
				WriteProblem(w, r, http.StatusBadRequest, "request.invalid", "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

var missingProperty = regexp.MustCompile(`property "([^"]+)" is missing`)

// fieldErrors раскладывает ошибку kin-openapi по полям. false — ошибка не про поля
// (тело не разбирается, неверный Content-Type): это request.invalid.
func fieldErrors(err error) ([]FieldError, bool) {
	errs := []error{err}
	if me, ok := err.(openapi3.MultiError); ok { //nolint:errorlint // только верхний уровень: вложенные MultiError — внутри RequestError
		errs = me
	}
	var out []FieldError
	for _, e := range errs {
		re, ok := errors.AsType[*openapi3filter.RequestError](e)
		if !ok {
			return nil, false
		}
		if _, ok := errors.AsType[*openapi3filter.ParseError](re.Err); ok {
			return nil, false
		}
		prefix := "body"
		if re.Parameter != nil {
			prefix = re.Parameter.In + "." + re.Parameter.Name
		}
		if errors.Is(re.Err, openapi3filter.ErrInvalidRequired) {
			out = append(out, FieldError{Field: prefix, Code: "required"})
			continue
		}
		schemaErrs := schemaErrors(re.Err)
		if len(schemaErrs) == 0 {
			return nil, false
		}
		for _, se := range schemaErrs {
			out = append(out, FieldError{Field: fieldPath(prefix, se), Code: se.SchemaField})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Code < out[j].Code
	})
	return slices.Compact(out), true
}

func schemaErrors(err error) []*openapi3.SchemaError {
	if me, ok := err.(openapi3.MultiError); ok { //nolint:errorlint // MultiError — срез, раскрываем сами
		var out []*openapi3.SchemaError
		for _, e := range me {
			out = append(out, schemaErrors(e)...)
		}
		return out
	}
	if se, ok := errors.AsType[*openapi3.SchemaError](err); ok {
		return []*openapi3.SchemaError{se}
	}
	return nil
}

// fieldPath — путь поля через точку (body.items.0.name). Для required kin-openapi может
// указывать на объект-родитель — тогда имя пропавшего свойства берётся из Reason.
func fieldPath(prefix string, se *openapi3.SchemaError) string {
	ptr := se.JSONPointer()
	if se.SchemaField == "required" {
		if m := missingProperty.FindStringSubmatch(se.Reason); m != nil && (len(ptr) == 0 || ptr[len(ptr)-1] != m[1]) {
			ptr = append(ptr, m[1])
		}
	}
	if len(ptr) == 0 {
		return prefix
	}
	return prefix + "." + strings.Join(ptr, ".")
}
```

- [ ] **Step 5: Запустить — проходит**

Run: `go test ./internal/platform/httpx/ -count=1`
Expected: PASS. Если упал единственный кейс и по выводу видно, что kin-openapi кладёт имя поля иначе (например, `kind` уже в `JSONPointer()` у required), поправить `fieldPath`, не тест: тест фиксирует формат контракта `Problem.errors`.

- [ ] **Step 6: Middleware бинарника в роутере**

`backend/internal/platform/httpx/router.go` — сигнатура и первая строка тела:

```go
// NewRouter — роутер с общим набором middleware. mws — middleware бинарника после общих
// (лимит тела, валидация по контракту); добавить их позже нельзя: chi запрещает Use после
// маршрутов. Бинарники только монтируют свои маршруты.
func NewRouter(log *slog.Logger, mws ...func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, accessLog(log), recoverer(log))
	r.Use(mws...)
```

(остальное тело — без изменений).

- [ ] **Step 7: Сборка обработчиков с валидацией; build возвращает ошибку**

`backend/internal/httpapi/public/handler.go`:

```go
package public

import (
	"fmt"
	"log/slog"
	"net/http"

	"wf/backend/internal/httpapi/public/oapi"
	"wf/backend/internal/platform/httpx"
)

// NewHandler собирает публичный API на общей HTTP-платформе: лимит тела и проверка
// запроса по контракту стоят до strict-хендлера.
func NewHandler(log *slog.Logger) (http.Handler, error) {
	spec, err := oapi.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("public: контракт: %w", err)
	}
	validate, err := httpx.ValidateRequests(spec)
	if err != nil {
		return nil, err
	}
	strict := oapi.NewStrictHandlerWithOptions(Server{}, nil, oapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  httpx.RequestErrorHandler,
		ResponseErrorHandlerFunc: httpx.ResponseErrorHandler(log),
	})
	return oapi.HandlerFromMux(strict, httpx.NewRouter(log, httpx.LimitBody(httpx.MaxBodyBytes), validate)), nil
}
```

`backend/internal/httpapi/admin/handler.go` — то же для `admin` (`fmt.Errorf("admin: контракт: %w", err)`, комментарий «API админки»).

В `handler_test.go` обоих пакетов: в `TestHealthMatchesContract` заменить вызов на

```go
	h, err := public.NewHandler(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	rec := v.Do(t, h, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/health", nil))
```

и добавить тест (в `admin` — с `admin.NewHandler`):

```go
// Валидатор пропускает маршрут не из контракта — 404 отвечает роутер в формате Problem.
func TestUnknownRouteIsProblemThroughValidator(t *testing.T) {
	h, err := public.NewHandler(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/nope", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"http.not_found"`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
```

(в импорты — `"strings"`).

`backend/internal/platform/server/server.go` — `build` возвращает ошибку, проверяется до открытия порта:

```go
func RunHTTP(ctx context.Context, name string, c HTTPConfig, logOut io.Writer,
	build func(log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error)) error {
	log := logx.New(logOut, c.Log).With("service", name)
	pool, err := db.Open(ctx, c.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	h, err := build(log, pool)
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", c.HTTP.Addr)
	if err != nil {
		return err
	}
	log.Info("слушаю", "addr", ln.Addr().String())
	return httpx.Serve(ctx, ln, h, c.HTTP.ShutdownTimeout)
}
```

В `server_test.go` замыкание возвращает `(http.Handler, error)`:

```go
		}, io.Discard, func(_ *slog.Logger, _ *pgxpool.Pool) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), nil
		})
```

`backend/cmd/api/main.go`:

```go
	return server.RunHTTP(ctx, "api", server.HTTPConfig(cfg), logOut,
		func(log *slog.Logger, _ *pgxpool.Pool) (http.Handler, error) { return public.NewHandler(log) })
```

`backend/cmd/admin-api/main.go` — то же с `admin.NewHandler`.

- [ ] **Step 8: Запустить — проходит**

Run: `./task backend:test`
Expected: PASS всё, включая `archtest`, `cmd/*`, `server`.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/platform/httpx backend/internal/platform/server backend/internal/httpapi backend/cmd/api/main.go backend/cmd/admin-api/main.go
git commit -m "httpx: валидация запросов по контракту (validation.failed с полями), лимит тела" -- backend/internal/platform/httpx backend/internal/platform/server backend/internal/httpapi backend/cmd/api/main.go backend/cmd/admin-api/main.go
```

---

### Task 8: Текст для каждого кода ошибки в локалях веба и админки

**Files:**
- Modify: `packages/i18n/src/index.ts`, `packages/i18n/src/index.test.ts`
- Create: `contracts/src/error-codes.mjs`, `contracts/src/error-codes.d.mts`
- Modify: `contracts/package.json` (`exports`), `apps/web/package.json`, `apps/admin/package.json`, `pnpm-lock.yaml`
- Modify: `apps/web/messages/{ru,en}.json`, `apps/admin/messages/{ru,en}.json`
- Test: `apps/web/src/i18n/error-codes.test.ts`, `apps/admin/src/i18n/error-codes.test.ts`

**Interfaces:**
- Consumes: `errorCodes(doc)` (Task 6).
- Produces: `missingKeys(m: Messages, keys: string[]): string[]` в `@wf/i18n`; `loadErrorCodes(name: 'public' | 'admin'): Promise<string[]>` в `@wf/contracts/error-codes`. Правило (спека §6.8, §12.5): для каждого кода `x-error-codes-common` и `x-error-codes` контракта в `messages/ru.json` приложения есть строка `errors.<код>` (точки кода — уровни вложенности JSON); `en` — те же ключи (существующий тест `diffKeys`).

- [ ] **Step 1: Падающий тест `missingKeys`**

В `packages/i18n/src/index.test.ts` импорт заменить на `import { diffKeys, missingKeys, withFallback } from './index';` и дописать в конец:

```ts
describe('missingKeys', () => {
  const m = { errors: { internal: 'Сбой', request: { invalid: 'Плохой запрос' } } };
  test('вложенные ключи через точку', () => {
    expect(missingKeys(m, ['errors.request.invalid', 'errors.internal', 'errors.http.not_found'])).toEqual([
      'errors.http.not_found',
    ]);
  });
  test('группа — не строка', () => {
    expect(missingKeys(m, ['errors.request'])).toEqual(['errors.request']);
  });
});
```

Run: `pnpm --filter @wf/i18n test` — FAIL: `missingKeys` не экспортируется.

- [ ] **Step 2: Реализация `missingKeys`**

В `packages/i18n/src/index.ts` дописать в конец:

```ts
// missingKeys — ключи из keys, для которых в m нет строки (уровни вложенности — через точку).
export function missingKeys(m: Messages, keys: string[]): string[] {
  const have = new Set(paths(m));
  return keys.filter((k) => !have.has(k)).sort();
}
```

Run: `pnpm --filter @wf/i18n test` — PASS.

- [ ] **Step 3: Коды ошибок контракта как экспорт `@wf/contracts`**

`contracts/src/error-codes.mjs`:

```js
// Коды ошибок собранного контракта — для проверки локалей приложений (спека §12.5).
import { readFile } from 'node:fs/promises';
import { parse } from 'yaml';
import { errorCodes } from './conventions.mjs';

export async function loadErrorCodes(name) {
  const doc = parse(await readFile(new URL(`../openapi/${name}.yaml`, import.meta.url), 'utf8'));
  return errorCodes(doc);
}
```

`contracts/src/error-codes.d.mts`:

```ts
export function loadErrorCodes(name: 'public' | 'admin'): Promise<string[]>;
```

В `contracts/package.json` после `"type": "module"` добавить:

```json
  "exports": {
    "./error-codes": {
      "types": "./src/error-codes.d.mts",
      "default": "./src/error-codes.mjs"
    }
  },
```

Run (из корня, один процесс pnpm): `pnpm --filter @wf/web --filter @wf/admin add -D "@wf/contracts@workspace:*"`
Expected: в `devDependencies` обоих приложений — `"@wf/contracts": "workspace:*"`, обновлён `pnpm-lock.yaml`.

- [ ] **Step 4: Падающие тесты приложений**

`apps/web/src/i18n/error-codes.test.ts`:

```ts
import { loadErrorCodes } from '@wf/contracts/error-codes';
import { missingKeys } from '@wf/i18n';
import { expect, test } from 'vitest';
import ru from '../../messages/ru.json';

test('у каждого кода ошибки публичного контракта есть текст', async () => {
  const codes = await loadErrorCodes('public');
  expect(codes).toContain('validation.failed');
  expect(missingKeys(ru, codes.map((c) => `errors.${c}`))).toEqual([]);
});
```

`apps/admin/src/i18n/error-codes.test.ts` — то же с `loadErrorCodes('admin')` и названием «…контракта админки…».

Run: `pnpm --filter @wf/web --filter @wf/admin test`
Expected: FAIL — в `missingKeys` все шесть кодов (`errors.http.method_not_allowed`, `errors.http.not_found`, `errors.internal`, `errors.request.invalid`, `errors.request.too_large`, `errors.validation.failed`).

- [ ] **Step 5: Тексты ошибок**

В `apps/web/messages/ru.json` и `apps/admin/messages/ru.json` добавить группу верхнего уровня:

```json
  "errors": {
    "internal": "Что-то пошло не так. Попробуйте ещё раз",
    "request": {
      "invalid": "Не удалось обработать запрос",
      "too_large": "Слишком большой запрос"
    },
    "validation": { "failed": "Проверьте поля формы" },
    "http": {
      "not_found": "Не найдено",
      "method_not_allowed": "Это действие недоступно"
    }
  }
```

В `apps/web/messages/en.json` и `apps/admin/messages/en.json`:

```json
  "errors": {
    "internal": "Something went wrong. Please try again",
    "request": {
      "invalid": "The request could not be processed",
      "too_large": "The request is too large"
    },
    "validation": { "failed": "Please check the form fields" },
    "http": {
      "not_found": "Not found",
      "method_not_allowed": "This action is not available"
    }
  }
```

- [ ] **Step 6: Запустить — проходит; подсадка**

Run: `pnpm --filter @wf/web --filter @wf/admin test` — PASS (включая `в en те же ключи, что в ru`).
Подсадка: в `contracts/openapi/public/root.yaml` временно добавить в `x-error-codes-common` строку `- planted.code`, `pnpm --filter @wf/contracts bundle`, `pnpm --filter @wf/web test` — FAIL `errors.planted.code`. Убрать строку, снова `pnpm --filter @wf/contracts bundle`, прогон — PASS; `git diff --stat -- contracts/openapi` пуст.

- [ ] **Step 7: Commit**

```bash
git add packages/i18n/src contracts/src/error-codes.mjs contracts/src/error-codes.d.mts contracts/package.json apps/web/package.json apps/admin/package.json pnpm-lock.yaml apps/web/messages apps/admin/messages apps/web/src/i18n/error-codes.test.ts apps/admin/src/i18n/error-codes.test.ts
git commit -m "Локали: текст для каждого кода ошибки контракта, проверка в вебе и админке" -- packages/i18n/src contracts/src/error-codes.mjs contracts/src/error-codes.d.mts contracts/package.json apps/web/package.json apps/admin/package.json pnpm-lock.yaml apps/web/messages apps/admin/messages apps/web/src/i18n/error-codes.test.ts apps/admin/src/i18n/error-codes.test.ts
```

---

### Task 9: Ломающие изменения контракта — oasdiff в CI

**Files:**
- Modify: `tools/go.mod`, `tools/go.sum`, `scripts/bootstrap.sh`
- Create: `scripts/contracts-breaking.sh`
- Modify: `Taskfile.yml`, `.github/workflows/backend.yml`

**Interfaces:**
- Consumes: бандлы `contracts/openapi/{public,admin}.yaml`.
- Produces: `./task contracts:breaking` — `oasdiff breaking` каждого бандла против `$CONTRACTS_BASE` (по умолчанию `origin/main`), код выхода 1 при ломающем изменении; в CI бэкенда — против ветки PR или коммита до пуша.

- [ ] **Step 1: Подключить oasdiff**

Run (из корня): `go -C tools get -tool github.com/oasdiff/oasdiff@v1.32.1`
Expected: `github.com/oasdiff/oasdiff` в блоке `tool` у `tools/go.mod`.

В `scripts/bootstrap.sh` в первую команду сборки добавить пакет:

```bash
go -C tools build -o "$BIN/" \
  github.com/go-task/task/v3/cmd/task \
  github.com/rhysd/actionlint/cmd/actionlint \
  github.com/axllent/mailpit \
  github.com/oasdiff/oasdiff
```

Run: `bash scripts/bootstrap.sh` (он же выполнит `pnpm install` — других pnpm-процессов в этот момент не запускать)
Expected: `.tools/bin/oasdiff` (`oasdiff.exe` на Windows); `.tools/bin/oasdiff --version` печатает `1.32.1`.

- [ ] **Step 2: Убедиться, что oasdiff ловит ломающее изменение (подсадка до скрипта)**

```bash
mkdir -p .tools/tmp
printf 'openapi: 3.0.3\ninfo: {title: t, version: "1"}\npaths:\n  /v1/x:\n    get:\n      responses: {"200": {description: ok}}\n' > .tools/tmp/base.yaml
printf 'openapi: 3.0.3\ninfo: {title: t, version: "1"}\npaths: {}\n' > .tools/tmp/head.yaml
.tools/bin/oasdiff breaking .tools/tmp/base.yaml .tools/tmp/head.yaml --fail-on ERR; echo "exit=$?"
rm -f .tools/tmp/base.yaml .tools/tmp/head.yaml
```

Expected: сообщение об удалённом пути `GET /v1/x` и `exit=1`.

- [ ] **Step 3: Скрипт проверки**

`scripts/contracts-breaking.sh`:

```bash
#!/usr/bin/env bash
# Ломающие изменения контрактов (бандлов) относительно базового коммита — oasdiff (спека §8.3).
# База — $CONTRACTS_BASE (в CI: ветка PR или коммит до пуша), по умолчанию origin/main.
# Нет базы или бандла в ней (новая ветка, первый коммит контракта) — проверка пропускается.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE="${CONTRACTS_BASE:-origin/main}"
OASDIFF="$PWD/.tools/bin/oasdiff"
TMP="$PWD/.tools/tmp"
mkdir -p "$TMP"
if ! git rev-parse --verify --quiet "$BASE^{commit}" >/dev/null; then
  echo "contracts:breaking: базы $BASE нет — пропуск"
  exit 0
fi
status=0
for name in public admin; do
  file="contracts/openapi/$name.yaml"
  if ! git cat-file -e "$BASE:$file" 2>/dev/null; then
    echo "contracts:breaking: $file нет в $BASE — пропуск"
    continue
  fi
  git show "$BASE:$file" > "$TMP/contracts-base-$name.yaml"
  echo "contracts:breaking: $file против $BASE"
  "$OASDIFF" breaking "$TMP/contracts-base-$name.yaml" "$file" --fail-on ERR || status=1
  rm -f "$TMP/contracts-base-$name.yaml"
done
exit $status
```

Run: `git add --chmod=+x scripts/contracts-breaking.sh` (исполняемый в git, как `scripts/check-format.sh`).

В корневой `Taskfile.yml` после задачи `gen` добавить:

```yaml
  contracts:breaking:
    desc: Ломающие изменения контрактов относительно базы (CONTRACTS_BASE, по умолчанию origin/main)
    cmds: ['bash scripts/contracts-breaking.sh']
```

- [ ] **Step 4: Прогон против main до этого плана**

Run: `CONTRACTS_BASE=e8fccd1 ./task contracts:breaking` (коммит документов перед планом — последняя версия контрактов до разбиения)
Expected: exit 0 — разбиение, теги, `security` с явным `security: []` у health, необязательное `errors` в `Problem` не ломают клиентов. Если oasdiff всё же пометил уровнем ERR добавление глобальной `security` (у health она явно пустая — клиент не затронут): создать `contracts/oasdiff-err-ignore.txt` с шапкой-комментарием `# Принятые ERR oasdiff: строка вывода целиком — метод, путь, текст; причина — в комментарии над строкой` и скопированной строкой вывода, а в скрипте вызвать oasdiff с `--err-ignore contracts/oasdiff-err-ignore.txt`; повторный прогон — exit 0.

- [ ] **Step 5: Шаг в CI бэкенда**

`.github/workflows/backend.yml`: у `actions/checkout` добавить

```yaml
        with:
          fetch-depth: 0
```

и после `- run: ./task backend:ci`:

```yaml
      # ломающее изменение контракта валит PR; ломать — только в /v2 (спека §8.3)
      - run: ./task contracts:breaking
        env:
          CONTRACTS_BASE: ${{ github.event_name == 'pull_request' && format('origin/{0}', github.base_ref) || github.event.before }}
```

Run: `.tools/bin/actionlint .github/workflows/backend.yml` — без замечаний.

- [ ] **Step 6: Commit**

```bash
git add tools/go.mod tools/go.sum scripts/bootstrap.sh scripts/contracts-breaking.sh Taskfile.yml .github/workflows/backend.yml
git commit -m "oasdiff: проверка ломающих изменений контрактов в CI бэкенда" -- tools/go.mod tools/go.sum scripts/bootstrap.sh scripts/contracts-breaking.sh Taskfile.yml .github/workflows/backend.yml
```

(если создан `contracts/oasdiff-err-ignore.txt` — добавить его в оба списка путей).

---

### Task 10: Документация под стражи и раскладку контракта

**Files:**
- Modify: `docs/04-принципы-архитектуры.md`, `.claude/rules/backend.md`, `.claude/rules/contracts.md`, `docs/versions.md`, `CLAUDE.md`

**Interfaces:** —

- [ ] **Step 1: `docs/04-принципы-архитектуры.md`**

Раздел «Границы, которые проверяются автоматически» — пункт «Изоляция модулей» заменить на:

```markdown
- Изоляция модулей — `backend/internal/archtest`:
  - `modules.go` — слои модулей (`Layers`) и пары `intx` (`IntxPairs`): модуль импортирует
    только корневой пакет нижнего модуля и `intx` из списка; платформа не зависит от
    модулей; модуль берёт из `internal/httpapi` только сгенерированные типы `oapi` в своих
    `httpapi/` и `admin/`; каталог `internal/<имя>` вне `Layers` — ошибка;
  - `ownership.go` — владелец каждой таблицы, представления и функции схемы (`Schema`):
    SQL модуля (`internal/<модуль>/queries`) пишет только свои таблицы, чужое читает только
    из экспортированных представлений `<модуль>_read_*`; объект схемы без владельца или
    устаревшая запись карты — ошибка. Разбор SQL — `archtest/sqlscan` (`go-pgquery`, без cgo).
- Контракт: операцию с тегом `<модуль>` реализует хендлер этого модуля (`apitest.TagViolations`,
  тесты `internal/httpapi/{public,admin}`); конвенции бандлов — `contracts/test/conventions.test.mjs`
  (тег-модуль, `operationId` lowerCamel, `x-error-codes`, `Idempotency-Key` у мутирующих операций
  с аутентификацией, нет внешних `$ref`); у каждого кода ошибки есть текст в локалях веба и
  админки (`src/i18n/error-codes.test.ts`); ломающее изменение — `./task contracts:breaking`
  (oasdiff, в CI бэкенда).
- Запрос проверяется по контракту до strict-хендлера (`httpx.ValidateRequests`):
  `400 validation.failed` с `errors: [{field, code}]`.
```

- [ ] **Step 2: `.claude/rules/backend.md`**

Строку «…Стражи (§12) появляются с планом «фундамента»; до него автоматически проверяется только граница админки.» заменить на:

```markdown
- Стражи — `internal/archtest`: новый модуль → строка в `Layers` (`modules.go`); новая пара `intx` — только правкой спеки §4.4 и `IntxPairs`; новая таблица, представление или функция → строка с владельцем в `Schema` (`ownership.go`), экспортированное представление называется `<модуль>_read_<имя>`.
- Сгенерированные типы API — `internal/httpapi/{public,admin}/oapi`; `httpapi/{public,admin}.Server` только встраивает хендлеры модулей. Тег операции в контракте = модуль-реализатор.
```

- [ ] **Step 3: `.claude/rules/contracts.md`** — целиком:

```markdown
---
paths:
  - "contracts/**"
---

# Контракты

- Править исходники: `openapi/{public,admin}/root.yaml` (общие компоненты, реестр схем) и `openapi/{public,admin}/<модуль>/{paths,schemas}.yaml`. Бандлы `openapi/{public,admin}.yaml` — сгенерированные, руками не правятся.
- Изменил YAML → `./task gen` (бандлы redocly, Go, TS-типы) → закоммить сгенерированное в `contracts/`, `backend/`, `apps/`.
- Конвенции (тест `test/conventions.test.mjs`): один тег = модуль-реализатор (`platform` — операции платформы), `operationId` lowerCamel, у операции `x-error-codes`, у мутирующей операции с аутентификацией — `$ref: '#/components/parameters/IdempotencyKey'`; анонимная операция — `security: []`.
- Новый код ошибки → текст `errors.<код>` в `apps/web/messages` (публичный контракт) или `apps/admin/messages` (админский), `ru` и `en`.
- Схема `Problem` правится в `public/root.yaml` и `admin/root.yaml` одинаково (тест `test/contracts.test.mjs`).
- Ломающее изменение `public.yaml` — только в `/v2` и с новым major тега `contracts-vX.Y.Z`; проверка — `./task contracts:breaking` (oasdiff).
- Изменил `tokens/tokens.json` → `pnpm --filter @wf/tokens build` (входит в `./task gen`).
```

- [ ] **Step 4: `docs/versions.md`** — в таблицу версий добавить строки:

```markdown
| Redocly CLI | 2.57.0 | `contracts/package.json` (сборка бандлов контракта) |
| oasdiff | 1.32.1 | `tools/go.mod` |
| go-pgquery (pg_query в wasm) | v0.0.0-20250409022910-10ac41983c07 | `backend/go.mod` — та же версия, что у sqlc в `backend/tools/go.mod` |
```

- [ ] **Step 5: `CLAUDE.md`** — строку раздела File structure про `contracts/` заменить на:

```markdown
- `contracts/` — OpenAPI: исходники `openapi/{public,admin}/` по модулям, бандлы `public.yaml`, `admin.yaml` (сгенерированы) — источник истины; `tokens/tokens.json`
```

- [ ] **Step 6: Полный прогон тестов**

Run: `./task test`
Expected: PASS — пакеты, бэкенд, админка, веб.

- [ ] **Step 7: Commit**

```bash
git add docs/04-принципы-архитектуры.md .claude/rules/backend.md .claude/rules/contracts.md docs/versions.md CLAUDE.md
git commit -m "Доки: стражи модулей и контракта, раскладка контракта по модулям" -- docs/04-принципы-архитектуры.md .claude/rules/backend.md .claude/rules/contracts.md docs/versions.md CLAUDE.md
```
