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
