package archtest

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
)

// Волна 2, m2: river.Config собирается только в internal/platform/queue (ClientConfig — исполняющий клиент,
// InsertConfig — клиент только для вставки). Стражи TestWorkerRiverMaintenance/TestAdminRiverMaintenance проверяют
// riverConfig бинарника, а не место вызова: river.NewClient(…, &river.Config{…}) в cmd/* обошёл бы общую часть
// обслуживания незаметно. Тесты (*_test.go) строят свои клиенты — их страж не трогает.
func TestRiverConfigOnlyInQueue(t *testing.T) {
	root := os.DirFS("../..") // корень модуля backend; пути fs — со слэшами
	files := 0
	var violations []string
	for _, dir := range []string{"cmd", "internal"} {
		err := fs.WalkDir(root, dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == "internal/platform/queue" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := fs.ReadFile(root, path)
			if err != nil {
				return err
			}
			files++
			v, err := riverConfigViolations(path, src)
			if err != nil {
				return err
			}
			violations = append(violations, v...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// страж не вхолостую: обход дошёл до бинарников с River
	if files < 50 {
		t.Fatalf("просмотрено файлов: %d — обход пуст", files)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// riverConfigViolations видит все способы собрать river.Config мимо queue: литерал (с & и без), new(river.Config),
// объявление переменной этого типа, импорт под другим именем и точкой; тип в сигнатуре и указатель — не сборка.
func TestRiverConfigViolations(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"литерал по указателю", `package x
import "github.com/riverqueue/river"
var c = &river.Config{}`, 1},
		{"литерал значением", `package x
import "github.com/riverqueue/river"
func f() { _ = river.Config{Logger: nil} }`, 1},
		{"new", `package x
import "github.com/riverqueue/river"
func f() { _ = new(river.Config) }`, 1},
		{"переменная типа", `package x
import "github.com/riverqueue/river"
func f() { var c river.Config; _ = c }`, 1},
		{"импорт под другим именем", `package x
import rq "github.com/riverqueue/river"
var c = &rq.Config{}`, 1},
		{"импорт точкой", `package x
import . "github.com/riverqueue/river"
var c = &Config{}`, 1},
		{"тип в сигнатуре и указатель — не сборка", `package x
import "github.com/riverqueue/river"
func f(c *river.Config) *river.Config { var p *river.Config; _ = p; return c }`, 0},
		{"чужой Config", `package x
import "github.com/riverqueue/river/rivertype"
type Config struct{}
var a = &Config{}
var b = rivertype.JobRow{}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := riverConfigViolations("x.go", []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.want {
				t.Fatalf("нарушений %d, ждали %d: %v", len(got), c.want, got)
			}
			if c.want > 0 && !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "x.go:") }) {
				t.Fatalf("без позиции: %v", got)
			}
		})
	}
}
