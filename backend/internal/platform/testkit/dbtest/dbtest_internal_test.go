package dbtest

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/peterldowns/pgtestdb"
)

// fatalRecorder перехватывает Fatalf: requireServer должен упасть с понятным сообщением.
type fatalRecorder struct {
	testing.TB
	msg string
}

type fatalStop struct{}

func (r *fatalRecorder) Helper() {}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.msg = fmt.Sprintf(format, args...)
	panic(fatalStop{})
}

func TestRequireServerNamesAddressAndPortSource(t *testing.T) {
	// занимаем свободный порт и сразу освобождаем — на нём точно никого нет
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	host, port, _ := net.SplitHostPort(addr)

	rec := &fatalRecorder{TB: t}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fatalStop); !ok {
					panic(r)
				}
			}
		}()
		requireServer(rec, pgtestdb.Config{Host: host, Port: port})
	}()

	for _, want := range []string{addr, "WF_TEST_PG_PORT", "WF_PG_PORT"} {
		if !strings.Contains(rec.msg, want) {
			t.Errorf("сообщение не содержит %q: %s", want, rec.msg)
		}
	}
}
