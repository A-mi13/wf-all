package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// dbtestPath — пакет тестов с базой: его импорт делает каталог пакетом с базой (TestDBTestsAreParallel).
const dbtestPath = "wf/backend/internal/platform/testkit/dbtest"

// serialReason — решение «последовательно» над функцией теста: строка комментария «// serial: <причина>».
var serialReason = regexp.MustCompile(`^//\s*serial:\s*\S`)

// importsDBTest — файл импортирует testkit/dbtest (под любым именем, в том числе _).
func importsDBTest(name string, src []byte) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	if err != nil {
		return false, err
	}
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == dbtestPath {
			return true, nil
		}
	}
	return false, nil
}

// isTestName — имя функции теста по правилам go test: Test, затем конец имени или не строчная буква.
func isTestName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(r)
}

// testingT — имя параметра функции вида func(x *testing.T) ("" — сигнатура не теста). Синтаксически: импорт
// testing под другим именем в кодовой базе не встречается.
func testingT(fn *ast.FuncDecl) (string, bool) {
	ps := fn.Type.Params.List
	if fn.Recv != nil || fn.Type.Results != nil || len(ps) != 1 || len(ps[0].Names) > 1 {
		return "", false
	}
	star, ok := ps[0].Type.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return "", false
	}
	if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "testing" {
		return "", false
	}
	if len(ps[0].Names) == 0 {
		return "_", true
	}
	return ps[0].Names[0].Name, true
}

// firstIsParallel — первая инструкция тела — <t>.Parallel() того же параметра.
func firstIsParallel(fn *ast.FuncDecl, t string) bool {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	es, ok := fn.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Parallel" {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == t
}

// isSerial — над функцией есть строка «// serial: <причина>» с непустой причиной.
func isSerial(fn *ast.FuncDecl) bool {
	if fn.Doc == nil {
		return false
	}
	for _, c := range fn.Doc.List {
		if serialReason.MatchString(c.Text) {
			return true
		}
	}
	return false
}

// parallelViolations — функции Test* файла name (число проверенных) и нарушения: нет ни t.Parallel() первой
// инструкцией, ни «// serial: <причина>» над функцией, либо есть оба сразу. Подтесты не проверяются: у табличного
// подтеста окружение бывает общим с родителем, решение о нём — за автором теста.
func parallelViolations(name string, src []byte) (int, []string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return 0, nil, err
	}
	n := 0
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || !isTestName(fn.Name.Name) {
			continue
		}
		t, ok := testingT(fn)
		if !ok {
			continue
		}
		n++
		par, ser := firstIsParallel(fn, t), isSerial(fn)
		switch {
		case par && ser:
			out = append(out, fmt.Sprintf("%s: %s — и t.Parallel(), и «// serial:»: оставь одно решение",
				fset.Position(fn.Pos()), fn.Name.Name))
		case !par && !ser:
			out = append(out, fmt.Sprintf("%s: %s — тест пакета с базой без t.Parallel() первой строкой; "+
				"нельзя параллельно — комментарий «// serial: <причина>» над функцией",
				fset.Position(fn.Pos()), fn.Name.Name))
		}
	}
	return n, out, nil
}

// callsDBTestMain — в файле есть TestMain(m *testing.M), который зовёт dbtest.Main: через импорт dbtest под любым
// именем, а в самом пакете dbtest (его внутренние тесты) — просто Main.
func callsDBTestMain(name string, src []byte) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	local := ""
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == dbtestPath {
			local = path.Base(p)
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	inside := f.Name.Name == path.Base(dbtestPath)
	found := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				x, ok := fun.X.(*ast.Ident)
				found = found || ok && local != "" && x.Name == local && fun.Sel.Name == "Main"
			case *ast.Ident:
				found = found || inside && fun.Name == "Main"
			}
			return true
		})
	}
	return found, nil
}
