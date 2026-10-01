package httpx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Страж: коды платформы выдаются только константами из codes.go — иначе сверка
// PlatformCodes с контрактом (x-error-codes-common) не увидит новый код.
func TestProblemCodesAreConstants(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range codeLiterals(f) {
			t.Errorf("%s: код ошибки строкой — нужна константа из codes.go", fset.Position(lit.Pos()))
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("нет исходников httpx — страж проверяет вхолостую")
	}
}

// Подсадка бага: литерал в WriteProblem и в поле Code у Problem ловится, константа — нет.
func TestCodeLiterals(t *testing.T) {
	src := `package x
func f() {
	WriteProblem(w, r, 400, "request.invalid", "")
	WriteProblem(w, r, 400, CodeRequestInvalid, "")
	_ = Problem{Status: 400, Code: "validation.failed"}
	_ = Problem{Status: 400, Code: CodeValidationFailed}
	_ = FieldError{Field: "query.limit", Code: "type"} // правило схемы, не код ошибки
}`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(codeLiterals(f)); got != 2 {
		t.Fatalf("найдено %d литералов, ожидалось 2", got)
	}
}

// codeLiterals — строковые литералы на месте кода ошибки: аргумент code у WriteProblem
// и поле Code у Problem{…} (Code у FieldError — правило схемы, не код ошибки).
func codeLiterals(f *ast.File) []*ast.BasicLit {
	var out []*ast.BasicLit
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "WriteProblem" && len(v.Args) > 3 {
				if lit, ok := v.Args[3].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					out = append(out, lit)
				}
			}
		case *ast.CompositeLit:
			if typ, ok := v.Type.(*ast.Ident); !ok || typ.Name != "Problem" {
				return true
			}
			for _, el := range v.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Code" {
					if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						out = append(out, lit)
					}
				}
			}
		}
		return true
	})
	return out
}
