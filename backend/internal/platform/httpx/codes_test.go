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
		for _, lit := range codeLiterals(f) {
			t.Errorf("%s: код ошибки строкой — нужна константа из httpx/codes.go", fset.Position(lit.Pos()))
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

// Подсадка бага: литерал в WriteProblem, NewError и в поле Code у Problem ловится — с префиксом
// пакета и без, константа — нет.
func TestCodeLiterals(t *testing.T) {
	src := `package x
func f() {
	WriteProblem(w, r, 400, "request.invalid", "")
	WriteProblem(w, r, 400, CodeRequestInvalid, "")
	httpx.WriteProblem(w, r, 400, "request.invalid", "")
	_ = Problem{Status: 400, Code: "validation.failed"}
	_ = Problem{Status: 400, Code: CodeValidationFailed}
	_ = httpx.Problem{Status: 429, Code: "ratelimit.exceeded"}
	_ = NewError(409, "idempotency.in_progress")
	_ = httpx.NewError(409, "idempotency.in_progress")
	_ = httpx.NewError(409, httpx.CodeIdempotencyInProgress)
	_ = FieldError{Field: "query.limit", Code: "type"} // правило схемы, не код ошибки
}`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(codeLiterals(f)); got != 6 {
		t.Fatalf("найдено %d литералов, ожидалось 6", got)
	}
}

// codeLiterals — строковые литералы на месте кода ошибки: аргумент code у WriteProblem (4-й) и
// NewError (2-й), с префиксом пакета и без, и поле Code у Problem{…}/httpx.Problem{…}
// (Code у FieldError — правило схемы, не код ошибки).
func codeLiterals(f *ast.File) []*ast.BasicLit {
	var out []*ast.BasicLit
	str := func(e ast.Expr) {
		if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			out = append(out, lit)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			switch name := funcName(v.Fun); {
			case name == "WriteProblem" && len(v.Args) > 3:
				str(v.Args[3])
			case name == "NewError" && len(v.Args) > 1:
				str(v.Args[1])
			}
		case *ast.CompositeLit:
			if funcName(v.Type) != "Problem" {
				return true
			}
			for _, el := range v.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Code" {
						str(kv.Value)
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
