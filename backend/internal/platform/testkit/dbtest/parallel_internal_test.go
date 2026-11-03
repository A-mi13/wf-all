package dbtest

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// Пакет с базой без явного -parallel идёт по DefaultParallel, а не по GOMAXPROCS: на 24 ядрах 24 теста разом
// клонируют и удаляют базы и пакет идёт вдвое дольше последовательного (S1, замер 03.11). Явный -parallel (в том
// числе через GOFLAGS и ./task) не трогается.
func TestDefaultParallel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"не задан", nil, "4"},
		{"задан явно", []string{"-test.parallel=1"}, "1"},
		{"задан явно равным умолчанию", []string{"-test.parallel", "4"}, "4"},
		{"задан явно больше", []string{"-test.parallel=16"}, "16"},
		{"другие флаги", []string{"-test.v=true"}, "4"},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.Int("test.parallel", 24, "")
		fs.Bool("test.v", false, "")
		if err := defaultParallel(fs, c.args, DefaultParallel); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := fs.Lookup("test.parallel").Value.String(); got != c.want {
			t.Errorf("%s: -test.parallel=%s, ждали %s", c.name, got, c.want)
		}
	}
	// флаги уже разобраны (testing.Init и Parse до Main) — явное значение видно и после разбора
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Int("test.parallel", 24, "")
	if err := fs.Parse([]string{"-test.parallel=2"}); err != nil {
		t.Fatal(err)
	}
	if err := defaultParallel(fs, []string{"-test.parallel=99"}, DefaultParallel); err != nil {
		t.Fatal(err)
	}
	if got := fs.Lookup("test.parallel").Value.String(); got != "2" {
		t.Errorf("разобранные флаги: -test.parallel=%s, ждали 2 (без повторного разбора)", got)
	}
	// флага нет (не тестовый бинарник) — ошибка Set, а не тихий пропуск
	if err := defaultParallel(flag.NewFlagSet("x", flag.ContinueOnError), nil, DefaultParallel); err == nil {
		t.Error("нет флага test.parallel — ждали ошибку")
	}
}

// parallelProbeEnv — метка дочернего процесса TestMainSetsDefaultParallel: он только печатает -test.parallel.
const parallelProbeEnv = "WF_DBTEST_PARALLEL_PROBE"

var probeLine = regexp.MustCompile(`(?m)^dbtest-parallel=(\d+)$`)

// Main этого пакета (TestMain) ставит умолчание процессу тестов. ./task и CI передают -parallel явно, поэтому проверка
// — в дочернем процессе того же бинарника БЕЗ -test.parallel (ревью S1 I1): иначе связка «Main → умолчание» не
// проверялась бы ни в одном автоматическом прогоне.
func TestMainSetsDefaultParallel(t *testing.T) {
	t.Parallel()
	if os.Getenv(parallelProbeEnv) == "1" {
		fmt.Printf("dbtest-parallel=%s\n", flag.Lookup("test.parallel").Value.String())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// без -test.parallel: умолчание ставит только Main
	args := []string{"-test.run=^TestMainSetsDefaultParallel$", "-test.count=1"}
	cmd := exec.CommandContext(ctx, os.Args[0], args...) //nolint:gosec // G204: свой же тестовый бинарник
	cmd.Env = append(os.Environ(), parallelProbeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("дочерний процесс: %v\n%s", err, out)
	}
	m := probeLine.FindSubmatch(out)
	if m == nil {
		t.Fatalf("дочерний процесс не напечатал -test.parallel:\n%s", out)
	}
	if got := string(m[1]); got != strconv.Itoa(DefaultParallel) {
		t.Fatalf("дочерний процесс без -parallel: -test.parallel=%s, ждали %d — Main не ставит умолчание", got, DefaultParallel)
	}
}
