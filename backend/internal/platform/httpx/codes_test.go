package httpx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Страж: коды ошибок платформы выдаются только константами из codes.go — иначе сверка
// PlatformCodes с контрактом (x-error-codes-common) не увидит новый код. Проверяются все
// пакеты платформы: rate limit, антибот, идемпотентность отвечают теми же кодами.
func TestProblemCodesAreConstants(t *testing.T) {
	consts := codeConsts(t)
	fset := token.NewFileSet()
	seen := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testkit" || strings.HasSuffix(name, "db") && name != "db" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, e := range badCodes(f, consts) {
			t.Errorf("%s: код ошибки не константой из httpx/codes.go (вне httpx — только httpx.Code…)",
				fset.Position(e.Pos()))
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 10 {
		t.Fatalf("просмотрено %d файлов платформы — страж проверяет вхолостую", seen)
	}
}

// Подсадка бага. Внутри httpx ловится строковый литерал в WriteProblem, NewError и поле Code у
// Problem; константа и параметр функции — нет.
func TestCodeLiteralsInsideHttpx(t *testing.T) {
	src := `package httpx
func f(code string) {
	WriteProblem(w, r, 400, "request.invalid", "")
	WriteProblem(w, r, 400, CodeRequestInvalid, "")
	_ = Problem{Status: 400, Code: "validation.failed"}
	_ = Problem{Status: 400, Code: CodeValidationFailed}
	_ = Problem{Status: 400, Code: code}
	_ = NewError(409, "idempotency.in_progress")
	_ = NewError(409, CodeIdempotencyInProgress)
	_ = FieldError{Field: "query.limit", Code: "type"} // правило схемы, не код ошибки
}`
	if got := len(badCodes(parseSrc(t, src), codeConsts(t))); got != 3 {
		t.Fatalf("найдено %d нарушений, ожидалось 3", got)
	}
}

// Подсадка бага. Вне httpx код — только httpx.Code* из codes.go: литерал, локальная константа,
// чужой пакет и несуществующая константа ловятся.
func TestCodeLiteralsOutsideHttpx(t *testing.T) {
	src := `package x
const codeX = "feature.disabled"
func f() {
	httpx.WriteProblem(w, r, 400, "request.invalid", "")
	httpx.WriteProblem(w, r, 400, httpx.CodeRequestInvalid, "")
	_ = httpx.Problem{Status: 429, Code: "ratelimit.exceeded"}
	_ = httpx.Problem{Status: 429, Code: httpx.CodeRateLimited}
	_ = httpx.NewError(409, "idempotency.in_progress")
	_ = httpx.NewError(409, httpx.CodeIdempotencyInProgress)
	_ = httpx.NewError(403, codeX)
	_ = httpx.NewError(403, other.CodeFeatureDisabled)
	_ = httpx.NewError(403, httpx.CodeNoSuchCode)
	_ = httpx.FieldError{Field: "query.limit", Code: "type"} // правило схемы, не код ошибки
}`
	if got := len(badCodes(parseSrc(t, src), codeConsts(t))); got != 6 {
		t.Fatalf("найдено %d нарушений, ожидалось 6", got)
	}
}

func parseSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// codeConsts — имена констант из codes.go: только они годятся вне httpx как httpx.<имя>.
func codeConsts(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "codes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, decl := range f.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.CONST {
			for _, spec := range gd.Specs {
				for _, name := range spec.(*ast.ValueSpec).Names {
					out[name.Name] = true
				}
			}
		}
	}
	if !out["CodeInternal"] {
		t.Fatalf("в codes.go не найдены константы кодов: %v", out)
	}
	return out
}

// badCodes — выражения на месте кода ошибки, нарушающие правило: аргумент code у WriteProblem
// (4-й) и NewError (2-й), с префиксом пакета и без, и поле Code у Problem{…}/httpx.Problem{…}
// (Code у FieldError — правило схемы, не код ошибки). Внутри httpx нарушение — строковый
// литерал (константы там без префикса, параметр функции пробрасывает чужой код). Вне httpx код
// — только httpx.<константа из codes.go>: локальная константа или переменная со строкой
// обходила бы сверку с контрактом.
func badCodes(f *ast.File, consts map[string]bool) []ast.Expr {
	inHttpx := f.Name.Name == "httpx"
	var out []ast.Expr
	check := func(e ast.Expr) {
		if inHttpx {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out = append(out, e)
			}
			return
		}
		if sel, ok := e.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "httpx" && consts[sel.Sel.Name] {
				return
			}
		}
		out = append(out, e)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			switch name := funcName(v.Fun); {
			case name == "WriteProblem" && len(v.Args) > 3:
				check(v.Args[3])
			case name == "NewError" && len(v.Args) > 1:
				check(v.Args[1])
			}
		case *ast.CompositeLit:
			if funcName(v.Type) != "Problem" {
				return true
			}
			for _, el := range v.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Code" {
						check(kv.Value)
					}
				}
			}
		}
		return true
	})
	return out
}

// funcName — имя без пакета: WriteProblem и httpx.WriteProblem одинаковы.
func funcName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}
