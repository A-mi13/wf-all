package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// mediaPath — корень модуля media: его NewRegistry собирает реестр целей (TestMediaPurposesOnlyFromRegistry).
const mediaPath = "wf/backend/internal/media"

// mediaRegistryCalls — упоминания media.NewRegistry в файле name (вызов и значение функции; импорт под другим именем и
// точкой учитывается). Синтаксический разбор без проверки типов.
func mediaRegistryCalls(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local, dot := "", false
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != mediaPath {
			continue
		}
		switch {
		case imp.Name == nil:
			local = "media"
		case imp.Name.Name == ".":
			dot = true
		case imp.Name.Name != "_":
			local = imp.Name.Name
		}
	}
	if local == "" && !dot {
		return nil, nil
	}
	var out []string
	report := func(n ast.Node) {
		out = append(out, fmt.Sprintf("%s: media.NewRegistry вне cmd/internal/mediapurposes — цели бинарника только "+
			"mediapurposes.Registry() (R12)", fset.Position(n.Pos())))
	}
	// имя после точки чужого селектора (events.NewRegistry) — не наш NewRegistry при импорте точкой
	sel := map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			sel[s.Sel] = true
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && local != "" && x.Name == local && n.Sel.Name == "NewRegistry" {
				report(n)
			}
		case *ast.Ident:
			if dot && n.Name == "NewRegistry" && !sel[n] {
				report(n)
			}
		}
		return true
	})
	return out, nil
}
