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
func TestMediaPurposesOnlyFromRegistry(t *testing.T) {
	t.Parallel()
	root := os.DirFS("../..")
	allowed := map[string]bool{"cmd/internal/mediapurposes": true, "internal/media/mediatest": true}
	files, calls := 0, 0
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
			if !allowed[path[:strings.LastIndex(path, "/")]] {
				violations = append(violations, v...)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// страж не вхолостую: обход дошёл до реестра бинарников
	if files < 50 || calls == 0 {
		t.Fatalf("просмотрено файлов %d, вызовов media.NewRegistry %d — обход пуст", files, calls)
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
