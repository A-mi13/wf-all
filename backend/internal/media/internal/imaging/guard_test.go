package imaging

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Ревью Task 7, m3: декодеры вызываются только явно по типу (decode, inspect). image.Decode/DecodeConfig выбирает
// декодер по сигнатуре среди зарегистрированных: в бинарнике с imaging это и deepteams/webp, и x/image/webp, и всё, что
// зарегистрирует чужой импорт, — мимо проверок типа, анимации и бюджета. Страж — по всему модулю backend (и тесты).
func TestNoGenericImageDecode(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..")) // backend/internal/media/internal/imaging → backend
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("корень модуля %s: %v", root, err)
	}
	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (strings.HasPrefix(n, ".") || n == "testdata" || n == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		found = append(found, genericDecodes(t, path)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("вызовы image.Decode/DecodeConfig (декодер — только явно по типу):\n%s", strings.Join(found, "\n"))
	}
}

// genericDecodes — вызовы Decode/DecodeConfig пакета "image" в файле (с учётом имени импорта).
func genericDecodes(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	name := ""
	var out []string
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == "image" {
			name = "image"
			if im.Name != nil {
				name = im.Name.Name
			}
			if name == "." { // Decode без селектора не отличить от своей функции — точечный импорт image запрещён целиком
				out = append(out, fset.Position(im.Pos()).String()+" (точечный импорт image)")
			}
		}
	}
	if name == "" || name == "_" || name == "." {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == name && (sel.Sel.Name == "Decode" || sel.Sel.Name == "DecodeConfig") {
			out = append(out, fset.Position(sel.Pos()).String())
		}
		return true
	})
	return out
}

// Страж сам ловит обходы (ревью R1, m2): алиас импорта и точечный импорт image; упоминание в строке и комментарии —
// не вызов.
func TestGenericDecodesCatches(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		src   string
		found bool
	}{
		"алиас":                {"package p\nimport img \"image\"\nvar _, _, _ = img.Decode(nil)\n", true},
		"точечный":             {"package p\nimport . \"image\"\nvar _, _, _ = Decode(nil)\n", true},
		"DecodeConfig":         {"package p\nimport \"image\"\nvar _, _, _ = image.DecodeConfig(nil)\n", true},
		"строка и комментарий": {"package p\nimport \"image\"\n// image.Decode\nvar _ = \"image.Decode\"\nvar _ image.Point\n", false},
		"пустой импорт":        {"package p\nimport _ \"image\"\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "p.go")
			if err := os.WriteFile(path, []byte(tc.src), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := genericDecodes(t, path); (len(got) > 0) != tc.found {
				t.Fatalf("найдено %v, ждали %v", got, tc.found)
			}
		})
	}
}
