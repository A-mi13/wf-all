package queue_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/queue"
)

// Слоты — сумма MaxWorkers очередей клиента: столько задач River может держать соединения пула разом.
func TestQueueSlots(t *testing.T) {
	t.Parallel()
	if n := queue.Slots(nil); n != 0 {
		t.Fatalf("Slots(nil) = %d", n)
	}
	if n := queue.Slots(map[string]river.QueueConfig{queue.Staff: {MaxWorkers: 5}}); n != 5 {
		t.Fatalf("Slots(staff) = %d", n)
	}
	// умолчания воркера (спека §9.1): 10 + 5 + 5 + 2 + 2 + 2 + 2
	if n := queue.Slots(queue.Config(config.Queues{Events: 10, Lifecycle: 5, Notify: 5, Mail: 2, Stats: 2, Media: 2, Maintenance: 2})); n != 28 {
		t.Fatalf("Slots(умолчания воркера) = %d, ждали 28", n)
	}
}

// План C, R4: слотов больше, чем пул минус запас (LISTEN River, лидер и обслуживание, HTTP или relay), — предупреждение,
// не отказ: задачи ждут соединения, локальные умолчания воркера (28 на пул 10) продолжают работать. Ровно на границе —
// без предупреждения.
func TestPoolWarning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		slots    int
		maxConns int32
		warn     bool
	}{
		{5, 10, false},                      // admin-api: staff 5 на пул 10
		{10 - queue.PoolReserve, 10, false}, // граница
		{10 - queue.PoolReserve + 1, 10, true},
		{28, 10, true}, // умолчания воркера
		{0, 2, true},   // пул меньше запаса
	}
	for _, c := range cases {
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		got := queue.WarnPool(log, c.slots, c.maxConns)
		logged := strings.Contains(buf.String(), "level=WARN")
		if got != c.warn || logged != c.warn {
			t.Errorf("слотов %d, пул %d: WarnPool = %v, WARN в журнале %v, ждали %v\n%s", c.slots, c.maxConns, got, logged, c.warn, buf.String())
		}
		if c.warn && (!strings.Contains(buf.String(), "slots=") || !strings.Contains(buf.String(), "DB_MAX_CONNS")) {
			t.Errorf("в предупреждении нет чисел или подсказки:\n%s", buf.String())
		}
	}
}
