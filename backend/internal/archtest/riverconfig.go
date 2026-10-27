package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// riverPath — пакет River, чей Config собирается только в internal/platform/queue (TestRiverConfigOnlyInQueue).
const riverPath = "github.com/riverqueue/river"

// riverConfigViolations — места файла name, где собирается river.Config: составной литерал (с & и без),
// new(river.Config), объявление переменной этого типа (нулевое значение, затем поля по одному). Тип в сигнатуре и
// указатель (*river.Config) — не сборка. Импорт River под другим именем и точкой учитывается. Синтаксический разбор
// без проверки типов: копию готового значения (c := *queue.ClientConfig(…); c.X = …) страж не увидит — это
// ловят стражи бинарников по riverConfig.
func riverConfigViolations(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local, dot := "", false
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != riverPath {
			continue
		}
		switch {
		case imp.Name == nil:
			local = "river"
		case imp.Name.Name == ".":
			dot = true
		case imp.Name.Name != "_":
			local = imp.Name.Name
		}
	}
	if local == "" && !dot {
		return nil, nil
	}
	isConfig := func(e ast.Expr) bool {
		switch t := e.(type) {
		case *ast.SelectorExpr:
			x, ok := t.X.(*ast.Ident)
			return ok && local != "" && x.Name == local && t.Sel.Name == "Config"
		case *ast.Ident:
			return dot && t.Name == "Config"
		}
		return false
	}
	var out []string
	report := func(n ast.Node, what string) {
		out = append(out, fmt.Sprintf("%s: %s river.Config вне internal/platform/queue — только queue.ClientConfig "+
			"(исполняющий клиент) или queue.InsertConfig (только вставка)", fset.Position(n.Pos()), what))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			if n.Type != nil && isConfig(n.Type) {
				report(n, "литерал")
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 && isConfig(n.Args[0]) {
				report(n, "new")
			}
		case *ast.ValueSpec:
			if n.Type != nil && isConfig(n.Type) {
				report(n, "переменная")
			}
		}
		return true
	})
	return out, nil
}
