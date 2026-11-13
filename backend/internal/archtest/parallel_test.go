package archtest

import (
	"io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

// S1 (03.11): тесты с базой идут параллельно — окружение теста (клон шаблона и DROP) стоит ~0,5 с, и пакет из
// сотни тестов подряд упирается в эту последовательность, а не в ядра. Пакет с базой — каталог, где хоть один
// *_test.go импортирует testkit/dbtest; каждая функция Test* такого каталога первой строкой зовёт t.Parallel()
// либо несёт над собой комментарий «// serial: <причина>» (глобальная переменная пакета, t.Setenv, порог
// реального времени). Новый тест без решения — красный страж, а не тихо последовательный прогон. Пакет с базой
// объявляет TestMain с dbtest.Main: умолчание -parallel (dbtest.DefaultParallel) вместо GOMAXPROCS.
func TestDBTestsAreParallel(t *testing.T) {
	t.Parallel()
	root := os.DirFS("../..") // корень модуля backend; пути fs — со слэшами
	files := map[string][]string{}
	for _, dir := range []string{"cmd", "internal", "migrations"} {
		err := fs.WalkDir(root, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, "_test.go") {
				files[path.Dir(p)] = append(files[path.Dir(p)], p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	pkgs, checked := 0, 0
	var violations []string
	for _, dir := range slices.Sorted(maps.Keys(files)) {
		srcs := map[string][]byte{}
		db := false
		for _, p := range files[dir] {
			src, err := fs.ReadFile(root, p)
			if err != nil {
				t.Fatal(err)
			}
			srcs[p] = src
			uses, err := importsDBTest(p, src)
			if err != nil {
				t.Fatal(err)
			}
			db = db || uses
		}
		if !db {
			continue
		}
		pkgs++
		withMain := false
		for _, p := range files[dir] {
			n, v, err := parallelViolations(p, srcs[p])
			if err != nil {
				t.Fatal(err)
			}
			checked += n
			violations = append(violations, v...)
			m, err := callsDBTestMain(p, srcs[p])
			if err != nil {
				t.Fatal(err)
			}
			withMain = withMain || m
		}
		if !withMain {
			violations = append(violations, dir+": пакет с базой без func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) } — "+
				"без него -parallel по умолчанию = GOMAXPROCS, и клоны баз душат Postgres")
		}
	}
	// страж не вхолостую: обход дошёл до пакетов с базой и их тестов
	if pkgs < 25 || checked < 700 {
		t.Fatalf("пакетов с базой %d, функций Test* %d — обход пуст или не видит тесты", pkgs, checked)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// parallelViolations: Test* без t.Parallel() первой строкой и без «// serial: <причина>» — нарушение; подтест,
// хелпер, TestMain, бенчмарк и Test-с-маленькой-буквой — не функции теста; t.Parallel() не первой строкой —
// нарушение (до него тест уже занял общее); пустая причина и оба решения сразу — нарушение.
func TestParallelViolations(t *testing.T) {
	t.Parallel()
	const head = "package p\n\nimport \"testing\"\n\n"
	cases := []struct {
		name    string
		src     string
		checked int
		want    int
	}{
		{"первой строкой", "func TestA(t *testing.T) {\n\tt.Parallel()\n\t_ = 1\n}\n", 1, 0},
		{"другое имя параметра", "func TestA(tt *testing.T) {\n\ttt.Parallel()\n}\n", 1, 0},
		{"нет вызова", "func TestA(t *testing.T) {\n\t_ = 1\n}\n", 1, 1},
		{"пустое тело", "func TestA(t *testing.T) {}\n", 1, 1},
		{"не первой строкой", "func TestA(t *testing.T) {\n\t_ = 1\n\tt.Parallel()\n}\n", 1, 1},
		{"Parallel чужого t", "func TestA(t *testing.T) {\n\tu := t\n\tu.Parallel()\n}\n", 1, 1},
		{"Parallel чужого объекта первой строкой", "func TestA(t *testing.T) {\n\tparent.Parallel()\n}\n", 1, 1},
		{"Parallel с аргументом", "func TestA(t *testing.T) {\n\tt.Parallel(1)\n}\n", 1, 1},
		{"в подтесте — не в счёт", "func TestA(t *testing.T) {\n\tt.Run(\"x\", func(t *testing.T) { t.Parallel() })\n}\n", 1, 1},
		{"serial с причиной", "// TestA — проверка.\n//\n// serial: меняет cleanupBatch пакета\nfunc TestA(t *testing.T) {}\n", 1, 0},
		{"serial без причины", "// serial:\nfunc TestA(t *testing.T) {}\n", 1, 1},
		{"serial не в начале строки", "// это не serial: причина\nfunc TestA(t *testing.T) {}\n", 1, 1},
		{"serial и Parallel", "// serial: причина\nfunc TestA(t *testing.T) {\n\tt.Parallel()\n}\n", 1, 1},
		{"параметр _", "func TestA(_ *testing.T) {}\n", 1, 1},
		{"TestMain", "func TestMain(m *testing.M) {}\n", 0, 0},
		{"хелпер", "func helper(t *testing.T) {}\n", 0, 0},
		{"Test с маленькой", "func Testify(t *testing.T) {}\n", 0, 0},
		{"бенчмарк", "func BenchmarkA(b *testing.B) {}\n", 0, 0},
		{"метод", "type s struct{}\n\nfunc (s) TestA(t *testing.T) {}\n", 0, 0},
		{"Test без суффикса", "func Test(t *testing.T) {\n\tt.Parallel()\n}\n", 1, 0},
		{"Test_ подчёркивание", "func Test_a(t *testing.T) {}\n", 1, 1},
	}
	for _, c := range cases {
		n, v, err := parallelViolations("x_test.go", []byte(head+c.src))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if n != c.checked || len(v) != c.want {
			t.Errorf("%s: функций %d, нарушений %d (%v), ждали %d и %d", c.name, n, len(v), v, c.checked, c.want)
		}
	}
}

// importsDBTest: только импорт testkit/dbtest (под любым именем) делает пакет пакетом с базой.
func TestImportsDBTest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		src  string
		want bool
	}{
		{"package p\n\nimport \"wf/backend/internal/platform/testkit/dbtest\"\n", true},
		{"package p\n\nimport db \"wf/backend/internal/platform/testkit/dbtest\"\n", true},
		{"package p\n\nimport _ \"wf/backend/internal/platform/testkit/dbtest\"\n", true},
		{"package p\n\nimport \"wf/backend/internal/platform/testkit/clocktest\"\n", false},
		{"package p\n", false},
	}
	for _, c := range cases {
		got, err := importsDBTest("x_test.go", []byte(c.src))
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%q: %v, ждали %v", c.src, got, c.want)
		}
	}
}

// callsDBTestMain: TestMain(m *testing.M), который зовёт dbtest.Main (под любым именем импорта; в самом пакете dbtest —
// Main); TestMain без dbtest.Main, хелпер с вызовом и Main другого пакета — нет.
func TestCallsDBTestMain(t *testing.T) {
	t.Parallel()
	const (
		imp   = "import (\n\t\"os\"\n\t\"testing\"\n\n\t\"wf/backend/internal/platform/testkit/dbtest\"\n)\n\n"
		plain = "import (\n\t\"os\"\n\t\"testing\"\n)\n\n"
	)
	cases := []struct {
		name, src string
		want      bool
	}{
		{"как положено", "package p\n\n" + imp + "func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }\n", true},
		{"другое имя импорта", "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\tdb \"wf/backend/internal/platform/testkit/dbtest\"\n)\n\n" +
			"func TestMain(m *testing.M) { os.Exit(db.Main(m)) }\n", true},
		{"внутри dbtest", "package dbtest\n\n" + plain + "func TestMain(m *testing.M) { os.Exit(Main(m)) }\n", true},
		{"m.Run напрямую", "package p\n\n" + imp + "var _ = dbtest.Config\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n", false},
		{"Main другого пакета", "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"wf/x/other\"\n)\n\n" +
			"func TestMain(m *testing.M) { os.Exit(other.Main(m)) }\n", false},
		{"Main другого пакета при импорте dbtest", "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"wf/backend/internal/platform/testkit/dbtest\"\n\t\"wf/x/other\"\n)\n\n" +
			"var _ = dbtest.Config\n\nfunc TestMain(m *testing.M) { os.Exit(other.Main(m)) }\n", false},
		{"Main не в TestMain", "package p\n\n" + imp + "func helper(m *testing.M) { os.Exit(dbtest.Main(m)) }\n", false},
		{"Main вне dbtest без импорта", "package p\n\n" + plain + "func TestMain(m *testing.M) { os.Exit(Main(m)) }\n", false},
		{"без TestMain", "package p\n\n" + imp + "var _ = dbtest.Main\n", false},
	}
	for _, c := range cases {
		got, err := callsDBTestMain("x_test.go", []byte(c.src))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: %v, ждали %v", c.name, got, c.want)
		}
	}
}
