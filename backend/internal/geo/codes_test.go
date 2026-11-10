package geo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Страж (ревью G4, m6): каждая экспортированная константа Code… пакета geo (любого файла, кроме тестов; хвост G4 n1 —
// раньше смотрел только codes.go) входит в PublicCodes или AdminCodes —
// иначе код ушёл бы клиенту без строки x-error-codes и текста, а TestGeoCodesDocumented и
// TestGeoAdminCodesDocumented сверяют контракты только со списками. Обратное направление (список → контракт) —
// у них.
func TestCodeConstantsListed(t *testing.T) {
	t.Parallel()
	consts := packageCodeConstants(t, ".")
	if len(consts) < len(AdminCodes) {
		t.Fatalf("в пакете найдено %d констант Code… — страж проверяет вхолостую", len(consts))
	}
	if bad := unlisted(consts); len(bad) != 0 {
		t.Fatalf("константы кодов вне PublicCodes и AdminCodes: %v", bad)
	}
}

// Подсадка бага: лишняя экспортированная константа кода без списка ловится; неэкспортированная и не Code… — нет.
func TestCodeConstantsListedCatchesStray(t *testing.T) {
	t.Parallel()
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

// Хвост G4 n1: константа кода в другом файле пакета тоже ловится; константы тестовых файлов — нет.
func TestCodeConstantsListedReadsWholePackage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"codes.go":  "package geo\nconst CodeCityNotFound = \"geo.city_not_found\"\n",
		"other.go":  "package geo\nconst CodeStray = \"geo.stray\"\n",
		"x_test.go": "package geo\nconst CodeTestOnly = \"geo.test_only\"\n",
		"notes.txt": "CodeText = geo.text",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := unlisted(packageCodeConstants(t, dir)); !slices.Equal(got, []string{"CodeStray = geo.stray"}) {
		t.Fatalf("подсадка в другом файле: %v", got)
	}
}

// packageCodeConstants — экспортированные строковые константы Code… всех файлов пакета в dir (без _test.go): имя →
// значение. Свой обход вместо parser.ParseDir (устарел: не учитывает теги сборки).
func packageCodeConstants(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		maps.Copy(out, codeConstants(t, filepath.Join(dir, name), nil))
	}
	return out
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
