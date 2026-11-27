package archtest

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"slices"
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

// mediatestPath — тестовые помощники media (цели e2e_image, MarkerCheck): только тесты и сборка wf_e2e (R12).
const mediatestPath = "wf/backend/internal/media/mediatest"

// e2eTag — тег сборки живого e2e.
const e2eTag = "wf_e2e"

// mediatestImports — импорт mediatest в файле name, который собирается без тега wf_e2e (нарушение R12), и число
// импортов mediatest вообще (страж не вхолостую). Файл без //go:build собирается всегда; с условием — если оно
// выполнимо хоть при одном наборе прочих тегов при wf_e2e = false.
func mediatestImports(name string, src []byte) (violations []string, imports int, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return nil, 0, err
	}
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == mediatestPath {
			imports++
			if !withoutE2E(f) {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s: импорт mediatest в файле прод-сборки — тестовые цели только "+
				"в _test.go и файлах //go:build %s (R12)", fset.Position(imp.Pos()), e2eTag))
		}
	}
	return violations, imports, nil
}

// withoutE2E — файл собирается хоть при одном наборе тегов с wf_e2e = false (условие //go:build до package).
func withoutE2E(f *ast.File) bool {
	var expr constraint.Expr
	for _, g := range f.Comments {
		if g.Pos() >= f.Package {
			break
		}
		for _, c := range g.List {
			if constraint.IsGoBuild(c.Text) {
				if e, err := constraint.Parse(c.Text); err == nil {
					expr = e
				}
			}
		}
	}
	if expr == nil {
		return true
	}
	tags := buildTags(expr, nil)
	// перебор наборов прочих тегов: в условиях сборки их единицы
	for mask := range 1 << min(len(tags), 16) {
		if expr.Eval(func(tag string) bool {
			i := slices.Index(tags, tag)
			return i >= 0 && mask&(1<<i) != 0
		}) {
			return true
		}
	}
	return false
}

// buildTags — теги условия, кроме wf_e2e, обходом дерева (Eval сокращает && и || и до части тегов не доходит).
func buildTags(e constraint.Expr, tags []string) []string {
	switch e := e.(type) {
	case *constraint.AndExpr:
		return buildTags(e.Y, buildTags(e.X, tags))
	case *constraint.OrExpr:
		return buildTags(e.Y, buildTags(e.X, tags))
	case *constraint.NotExpr:
		return buildTags(e.X, tags)
	case *constraint.TagExpr:
		if e.Tag != e2eTag && !slices.Contains(tags, e.Tag) {
			tags = append(tags, e.Tag)
		}
	}
	return tags
}
