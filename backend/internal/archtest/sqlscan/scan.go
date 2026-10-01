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
// объект другой схемы — "<схема>.<имя>". Последовательности, названные строкой
// (nextval('seq')), не отслеживаются: видна только сама функция.
type Usage struct {
	Writes    []string // INSERT, UPDATE, DELETE, MERGE, TRUNCATE; COPY — запись в обе стороны (и FROM, и TO)
	Reads     []string // FROM, JOIN, подзапросы — кроме имён CTE в их области видимости
	Functions []string // вызовы функций, кроме sqlc.*
}

// sqlc разрешает именованные параметры @name — для грамматики PostgreSQL это не SQL.
// Перед параметром не должно стоять @, иначе это оператор @@ (tsv @@to_tsquery(...)).
// Замена внутри строк и комментариев безвредна: разбор не ломается, имена таблиц там не ищутся.
var namedParam = regexp.MustCompile(`(^|[^@])@[A-Za-z_][A-Za-z0-9_]*`)

// Операторы верхнего уровня, которые страж понимает. В файлах запросов модуля — только DML;
// всё прочее (CALL, REFRESH, DDL) — ошибка: страж закрыт по умолчанию, а не молча пропускает.
var allowedStmts = map[string]bool{
	"SelectStmt": true, "InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true,
	"MergeStmt": true, "TruncateStmt": true, "CopyStmt": true,
}

// Операторы, у которых поле relation — цель записи.
var writeStmts = map[string]bool{
	"InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true, "MergeStmt": true, "CopyStmt": true,
}

// Scan разбирает SQL (файл запросов целиком, операторы через «;») и возвращает объекты схемы,
// которые он пишет, читает и вызывает. Ошибка — синтаксис не разобран, оператор верхнего уровня
// вне DML или SELECT ... INTO (создаёт таблицу).
func Scan(sql string) (Usage, error) {
	js, err := pgquery.ParseToJSON(namedParam.ReplaceAllString(sql, "${1}$$1"))
	if err != nil {
		return Usage{}, fmt.Errorf("sqlscan: %w", err)
	}
	var tree map[string]any
	if err := json.Unmarshal([]byte(js), &tree); err != nil {
		return Usage{}, fmt.Errorf("sqlscan: дерево разбора: %w", err)
	}
	for _, s := range asList(tree["stmts"]) {
		for kind := range asMap(asMap(s)["stmt"]) {
			if !allowedStmts[kind] {
				return Usage{}, fmt.Errorf("sqlscan: оператор %s не поддерживается — в запросах модуля только DML", kind)
			}
		}
	}
	w := &walker{
		writes: map[string]bool{},
		reads:  map[string]bool{},
		funcs:  map[string]bool{},
	}
	w.walk(tree, "", nil)
	if w.err != nil {
		return Usage{}, w.err
	}
	return Usage{Writes: sorted(w.writes), Reads: sorted(w.reads), Functions: sorted(w.funcs)}, nil
}

type walker struct {
	writes, reads, funcs map[string]bool
	err                  error
}

// walk обходит дерево; stmt — ближайший охватывающий узел-оператор (ключ вида InsertStmt),
// ctes — имена CTE, видимые в этой точке (только чтение: расширение — копией).
func (w *walker) walk(n any, stmt string, ctes map[string]bool) {
	switch v := n.(type) {
	case map[string]any:
		if wc, ok := v["withClause"]; ok {
			ctes = w.walkWith(wc, stmt, ctes)
		}
		for k, child := range v {
			switch {
			case k == "withClause":
				continue // уже обойдено в walkWith со своими областями видимости
			case k == "intoClause":
				w.fail(fmt.Errorf("sqlscan: SELECT ... INTO создаёт таблицу — не поддерживается"))
				continue
			case k == "lockedRels":
				continue // FOR UPDATE OF t — псевдонимы из FROM, а не таблицы
			case k == "RangeVar":
				w.add(child, w.reads, ctes)
			case k == "relation" && writeStmts[stmt]:
				w.add(child, w.writes, nil) // цель записи никогда не CTE
				continue
			case k == "relations" && stmt == "TruncateStmt":
				for _, item := range asList(child) {
					w.add(asMap(item)["RangeVar"], w.writes, nil)
				}
				continue // иначе те же RangeVar засчитаются ещё и чтением
			case k == "FuncCall":
				w.addFunc(child)
			}
			next := stmt
			if isStmtNode(k) {
				next = k
			}
			w.walk(child, next, ctes)
		}
	case []any:
		for _, c := range v {
			w.walk(c, stmt, ctes)
		}
	}
}

// walkWith обходит тела CTE и возвращает имена, видимые основному запросу.
// Без RECURSIVE тело CTE видит только предыдущие CTE того же WITH (своё имя там — таблица);
// с RECURSIVE — все CTE этого WITH, включая себя.
func (w *walker) walkWith(wc any, stmt string, outer map[string]bool) map[string]bool {
	m := asMap(wc)
	if inner, ok := m["WithClause"]; ok {
		m = asMap(inner)
	}
	recursive, _ := m["recursive"].(bool)
	var names []string
	var bodies []any
	for _, item := range asList(m["ctes"]) {
		cte := asMap(asMap(item)["CommonTableExpr"])
		name, _ := cte["ctename"].(string)
		names = append(names, name)
		bodies = append(bodies, cte["ctequery"])
	}
	all := withNames(outer, names...)
	for i, body := range bodies {
		scope := withNames(outer, names[:i]...)
		if recursive {
			scope = all
		}
		w.walk(body, stmt, scope)
	}
	return all
}

func withNames(base map[string]bool, names ...string) map[string]bool {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]bool{}
	}
	for _, n := range names {
		out[n] = true
	}
	return out
}

// isStmtNode — узел-оператор (InsertStmt), а не поле (selectStmt).
func isStmtNode(k string) bool {
	return len(k) > 4 && k[0] >= 'A' && k[0] <= 'Z' && strings.HasSuffix(k, "Stmt")
}

// add записывает отношение в set; неквалифицированное имя из ctes — CTE, не таблица.
func (w *walker) add(rel any, set, ctes map[string]bool) {
	m := asMap(rel)
	name, _ := m["relname"].(string)
	schema, _ := m["schemaname"].(string)
	switch {
	case name == "":
		return
	case schema == "" && ctes[name]:
		return
	case schema != "" && schema != "public":
		name = schema + "." + name
	}
	set[name] = true
}

func (w *walker) addFunc(fc any) {
	var parts []string
	for _, p := range asList(asMap(fc)["funcname"]) {
		if sval, ok := asMap(asMap(p)["String"])["sval"].(string); ok {
			parts = append(parts, sval)
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

func (w *walker) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sorted(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(set))
}
