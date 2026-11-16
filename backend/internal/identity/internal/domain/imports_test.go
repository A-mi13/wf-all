package domain_test

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Домен — чистые правила (план I-2, Task 7, приёмка): стандартная библиотека, x/text, uuid и пакеты платформы
// otp, emailaddr, password, events; без БД, HTTP и чужих модулей.
func TestDomainImports(t *testing.T) {
	allowed := map[string]bool{
		"github.com/google/uuid":                 true,
		"wf/backend/internal/platform/otp":       true,
		"wf/backend/internal/platform/emailaddr": true,
		"wf/backend/internal/platform/password":  true,
		"wf/backend/internal/platform/events":    true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fs, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(p, "/")
			std := !strings.Contains(first, ".") && first != "wf"
			if std && (strings.HasPrefix(p, "net/http") || p == "database/sql") {
				t.Errorf("%s: %s — домен без HTTP и БД", name, p)
			}
			if !std && !allowed[p] && !strings.HasPrefix(p, "golang.org/x/text/") {
				t.Errorf("%s: импорт %s вне списка домена", name, p)
			}
		}
	}
}
