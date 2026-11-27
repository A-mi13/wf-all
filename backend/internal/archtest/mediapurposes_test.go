package archtest

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// R12 плана media: набор целей одинаков у трёх бинарников по построению — media.NewRegistry вызывается только в
// cmd/internal/mediapurposes (прод и сборка wf_e2e) и в тестовых помощниках internal/media/mediatest (их импортируют
// только тесты и файлы cmd/* с тегом wf_e2e); тесты (*_test.go) — исключение. Файлы с тегами сборки разбираются все.
// Импорт mediatest вне его каталога — только в файлах, которые без тега wf_e2e не собираются (mediatestImports).
func TestMediaPurposesOnlyFromRegistry(t *testing.T) {
	t.Parallel()
	root := os.DirFS("../..")
	allowed := map[string]bool{"cmd/internal/mediapurposes": true, "internal/media/mediatest": true}
	files, calls, imports := 0, 0, 0
	var violations []string
	for _, dir := range []string{"cmd", "internal"} {
		err := fs.WalkDir(root, dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := fs.ReadFile(root, path)
			if err != nil {
				return err
			}
			files++
			v, err := mediaRegistryCalls(path, src)
			if err != nil {
				return err
			}
			calls += len(v)
			dir := path[:strings.LastIndex(path, "/")]
			if !allowed[dir] {
				violations = append(violations, v...)
			}
			if dir != "internal/media/mediatest" {
				iv, n, err := mediatestImports(path, src)
				if err != nil {
					return err
				}
				imports += n
				violations = append(violations, iv...)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// страж не вхолостую: обход дошёл до реестра бинарников
	// импорты mediatest сборки wf_e2e (mediae2e, mediapurposes/e2e.go) — разбор их видит
	if files < 50 || calls == 0 || imports == 0 {
		t.Fatalf("просмотрено файлов %d, вызовов media.NewRegistry %d, импортов mediatest %d — обход пуст", files, calls, imports)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

func TestMediaRegistryCalls(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src string
		want      int
	}{
		{"вызов", `package x
import "wf/backend/internal/media"
var r, _ = media.NewRegistry()`, 1},
		{"под другим именем", `package x
import m "wf/backend/internal/media"
func f() { _, _ = m.NewRegistry(m.Purpose{}) }`, 1},
		{"точкой", `package x
import . "wf/backend/internal/media"
var r, _ = NewRegistry()`, 1},
		{"значение функции", `package x
import "wf/backend/internal/media"
var f = media.NewRegistry`, 1},
		{"чужой NewRegistry", `package x
import "wf/backend/internal/platform/events"
var r, _ = events.NewRegistry()`, 0},
		{"точкой и чужой селектор", `package x
import . "wf/backend/internal/media"
import "wf/backend/internal/platform/events"
var r, _ = events.NewRegistry()
var p Purpose`, 0},
		{"подпакет media", `package x
import "wf/backend/internal/media/jobs"
var r = jobs.NewRegistry`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := mediaRegistryCalls("x.go", []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.want {
				t.Fatalf("вызовов %d, ждали %d: %v", len(got), c.want, got)
			}
		})
	}
}

// Ревью Task 10 m1: прод-файл (не _test.go и не только под тегом wf_e2e) не импортирует mediatest — иначе тестовые
// цели e2e_image попали бы в прод-сборку мимо стража NewRegistry. Условие сборки разбирается: файл нарушает, если
// собирается хоть при одном наборе тегов без wf_e2e.
func TestMediatestImports(t *testing.T) {
	t.Parallel()
	const imp = "import \"wf/backend/internal/media/mediatest\"\nvar r, _ = mediatest.Registry()\n"
	cases := []struct {
		name, src string
		want      int
	}{
		{"прод-файл", "package x\n" + imp, 1},
		{"под другим именем", "package x\nimport mt \"wf/backend/internal/media/mediatest\"\nvar r, _ = mt.Registry()\n", 1},
		{"пустой импорт", "package x\nimport _ \"wf/backend/internal/media/mediatest\"\n", 1},
		{"тег wf_e2e", "//go:build wf_e2e\n\npackage x\n" + imp, 0},
		{"wf_e2e и linux", "//go:build wf_e2e && linux\n\npackage x\n" + imp, 0},
		{"не wf_e2e", "//go:build !wf_e2e\n\npackage x\n" + imp, 1},
		{"wf_e2e или linux", "//go:build wf_e2e || linux\n\npackage x\n" + imp, 1},
		{"чужой тег", "//go:build !windows\n\npackage x\n" + imp, 1},
		{"два чужих тега", "//go:build linux && amd64\n\npackage x\n" + imp, 1},
		{"wf_e2e в глубине", "//go:build (linux || darwin) && wf_e2e\n\npackage x\n" + imp, 0},
		{"без импорта", "package x\nimport \"wf/backend/internal/media\"\nvar _ media.Purpose\n", 0},
		{"подпакет с тем же префиксом", "package x\nimport \"wf/backend/internal/media/mediatestx\"\nvar _ = mediatestx.X\n", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := mediatestImports("x.go", []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.want {
				t.Fatalf("нарушений %d, ждали %d: %v", len(got), c.want, got)
			}
		})
	}
}
