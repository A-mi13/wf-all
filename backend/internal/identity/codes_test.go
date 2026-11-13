package identity

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

// Страж (образец — geo/codes_test.go): каждая экспортированная константа Code… пакета identity (любого
// нетестового файла) входит хотя бы в один список — PublicCodes, AdminCodes или CrossModuleCodes. Иначе код ушёл бы
// клиенту без строки x-error-codes и текста: TestIdentityCodesDocumented, TestIdentityAdminCodesDocumented и
// TestCrossModuleCodesNotInIdentityOps сверяют контракты только со списками. Обратное направление (список →
// контракт) — у них.
func TestCodeConstantsListed(t *testing.T) {
	t.Parallel()
	consts := packageCodeConstants(t, ".")
	if len(consts) < len(CrossModuleCodes) {
		t.Fatalf("в пакете найдено %d констант Code… — страж проверяет вхолостую", len(consts))
	}
	if bad := unlisted(consts); len(bad) != 0 {
		t.Fatalf("константы кодов вне PublicCodes, AdminCodes и CrossModuleCodes: %v", bad)
	}
}

// Подсадка бага: лишняя экспортированная константа кода без списка ловится; константа из любого из трёх списков,
// неэкспортированная и не Code… — нет.
func TestCodeConstantsListedCatchesStray(t *testing.T) {
	t.Parallel()
	src := `package identity
const (
	CodeRestricted = "identity.restricted"
	CodeStray      = "identity.stray"
	codeLocal      = "identity.local"
	MaxThing       = "x"
)`
	got := unlisted(codeConstants(t, "x.go", src))
	if !slices.Equal(got, []string{"CodeStray = identity.stray"}) {
		t.Fatalf("подсадка: %v", got)
	}
}

// Константа кода в другом файле пакета тоже ловится; константы тестовых файлов — нет.
func TestCodeConstantsListedReadsWholePackage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"codes.go":  "package identity\nconst CodeRestricted = \"identity.restricted\"\n",
		"other.go":  "package identity\nconst CodeStray = \"identity.stray\"\n",
		"x_test.go": "package identity\nconst CodeTestOnly = \"identity.test_only\"\n",
		"notes.txt": "CodeText = identity.text",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := unlisted(packageCodeConstants(t, dir)); !slices.Equal(got, []string{"CodeStray = identity.stray"}) {
		t.Fatalf("подсадка в другом файле: %v", got)
	}
}

// Списки не пересекаются (кроме PublicCodes и AdminCodes: код вроде application_not_found отдают обе стороны), а
// CrossModuleCodes — ровно коды, которых нет в операциях identity: код из него в PublicCodes или AdminCodes
// означал бы, что identity сама его отдаёт (R9).
func TestCrossModuleCodesSeparate(t *testing.T) {
	t.Parallel()
	for _, c := range CrossModuleCodes {
		if slices.Contains(PublicCodes, c) || slices.Contains(AdminCodes, c) {
			t.Errorf("%s: код других модулей (CrossModuleCodes) в списке операций identity", c)
		}
	}
	for _, c := range slices.Concat(PublicCodes, AdminCodes, CrossModuleCodes) {
		if !strings.HasPrefix(c, "identity.") {
			t.Errorf("%s: код identity без префикса identity.", c)
		}
	}
}

// packageCodeConstants — экспортированные строковые константы Code… всех файлов пакета в dir (без _test.go): имя →
// значение.
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

// unlisted — «имя = значение» констант, которых нет ни в одном из трёх списков, по алфавиту.
func unlisted(consts map[string]string) []string {
	var bad []string
	for name, v := range consts {
		if !slices.Contains(PublicCodes, v) && !slices.Contains(AdminCodes, v) && !slices.Contains(CrossModuleCodes, v) {
			bad = append(bad, name+" = "+v)
		}
	}
	slices.Sort(bad)
	return bad
}
