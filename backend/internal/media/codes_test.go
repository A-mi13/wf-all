package media

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"wf/backend/internal/platform/httpx"
)

// Страж (образец — identity/codes_test.go, R18): каждая экспортированная константа Code… пакета media (любого
// нетестового файла) входит в OwnerCodes. Коды media отдают операции владельцев: в их x-error-codes и errors.media.*
// локалей фронтов они попадают по списку с первой операцией владельца (план economy).
func TestCodeConstantsListed(t *testing.T) {
	t.Parallel()
	consts := packageCodeConstants(t, ".")
	if len(consts) < 8 {
		t.Fatalf("в пакете найдено %d констант Code… — страж проверяет вхолостую", len(consts))
	}
	if bad := unlisted(consts); len(bad) != 0 {
		t.Fatalf("константы кодов вне OwnerCodes: %v", bad)
	}
	if len(OwnerCodes) != len(consts) {
		t.Fatalf("OwnerCodes: %d кодов, констант %d — дубль или код без константы", len(OwnerCodes), len(consts))
	}
}

// Подсадка бага: лишняя экспортированная константа кода без списка ловится, неэкспортированная и не Code… — нет;
// константа из другого файла пакета — тоже, тестового файла — нет.
func TestCodeConstantsListedCatchesStray(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"codes.go":  "package media\nconst (\n\tCodeUnavailable = \"media.unavailable\"\n\tcodeLocal = \"media.local\"\n\tMaxThing = \"x\"\n)\n",
		"other.go":  "package media\nconst CodeStray = \"media.stray\"\n",
		"x_test.go": "package media\nconst CodeTestOnly = \"media.test_only\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := unlisted(packageCodeConstants(t, dir)); !slices.Equal(got, []string{"CodeStray = media.stray"}) {
		t.Fatalf("подсадка: %v", got)
	}
}

// Формат кода — media.<snake>; без повторов.
func TestCodeFormat(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^media\.[a-z]+(_[a-z]+)*$`)
	seen := map[string]bool{}
	for _, c := range OwnerCodes {
		if !re.MatchString(c) || seen[c] {
			t.Errorf("%q: не media.<snake> или повтор", c)
		}
		seen[c] = true
	}
}

// Ошибки-значения — *httpx.Error со статусом и кодом спеки §7.2.
func TestCodeErrors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrUnavailable, http.StatusServiceUnavailable, "media.unavailable"},
		{ErrUnsupportedType, http.StatusUnprocessableEntity, "media.unsupported_type"},
		{ErrTooLarge, http.StatusUnprocessableEntity, "media.too_large"},
		{ErrTooManyPending, http.StatusTooManyRequests, "media.too_many_pending"},
		{ErrQuotaExceeded, http.StatusTooManyRequests, "media.quota_exceeded"},
		{ErrUploadIncomplete, http.StatusConflict, "media.upload_incomplete"},
		{ErrUploadExpired, http.StatusGone, "media.upload_expired"},
		{ErrObjectUnavailable, http.StatusConflict, "media.object_unavailable"},
	} {
		var he *httpx.Error
		if !errors.As(c.err, &he) {
			t.Fatalf("%v — не *httpx.Error", c.err)
		}
		if p := he.Problem(); p.Status != c.status || p.Code != c.code {
			t.Errorf("%s: статус %d, код %s; ждали %d %s", c.code, p.Status, p.Code, c.status, c.code)
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
		maps.Copy(out, codeConstants(t, filepath.Join(dir, name)))
	}
	return out
}

// codeConstants — экспортированные строковые константы Code… файла: имя → значение.
func codeConstants(t *testing.T, name string) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
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

// unlisted — «имя = значение» констант, которых нет в OwnerCodes, по алфавиту.
func unlisted(consts map[string]string) []string {
	var bad []string
	for name, v := range consts {
		if !slices.Contains(OwnerCodes, v) {
			bad = append(bad, name+" = "+v)
		}
	}
	slices.Sort(bad)
	return bad
}
