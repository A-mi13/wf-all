package geo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

// Страж (ревью G4, m6): каждая экспортированная константа Code… из codes.go входит в PublicCodes или AdminCodes —
// иначе код ушёл бы клиенту без строки x-error-codes и текста, а TestGeoCodesDocumented и
// TestGeoAdminCodesDocumented сверяют контракты только со списками. Обратное направление (список → контракт) —
// у них.
func TestCodeConstantsListed(t *testing.T) {
	consts := codeConstants(t, "codes.go", nil)
	if len(consts) < len(AdminCodes) {
		t.Fatalf("в codes.go найдено %d констант Code… — страж проверяет вхолостую", len(consts))
	}
	if bad := unlisted(consts); len(bad) != 0 {
		t.Fatalf("константы кодов вне PublicCodes и AdminCodes: %v", bad)
	}
}

// Подсадка бага: лишняя экспортированная константа кода без списка ловится; неэкспортированная и не Code… — нет.
func TestCodeConstantsListedCatchesStray(t *testing.T) {
	src := `package geo
const (
	CodeCityNotFound = "geo.city_not_found"
	CodeStray        = "geo.stray"
	codeLocal        = "geo.local"
	MaxThing         = "x"
)`
	got := unlisted(codeConstants(t, "x.go", src))
	if !slices.Equal(got, []string{"CodeStray = geo.stray"}) {
		t.Fatalf("подсадка: %v", got)
	}
}

// codeConstants — экспортированные строковые константы Code… файла: имя → значение.
func codeConstants(t *testing.T, name string, src any) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, id := range vs.Names {
				if !id.IsExported() || len(id.Name) < 5 || id.Name[:4] != "Code" || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: значение — не строковый литерал", id.Name)
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out[id.Name] = v
			}
		}
	}
	return out
}

// unlisted — «имя = значение» констант, которых нет ни в PublicCodes, ни в AdminCodes, по алфавиту.
func unlisted(consts map[string]string) []string {
	var bad []string
	for name, v := range consts {
		if !slices.Contains(PublicCodes, v) && !slices.Contains(AdminCodes, v) {
			bad = append(bad, name+" = "+v)
		}
	}
	slices.Sort(bad)
	return bad
}
