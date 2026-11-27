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
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == "image" {
			name = "image"
			if im.Name != nil {
				name = im.Name.Name
			}
		}
	}
	if name == "" || name == "_" {
		return nil
	}
	var out []string
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
