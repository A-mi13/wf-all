package workerhost

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

// serviceEnv — почта и кольцо identity, обязательные режиму воркера; SMTP — адрес, на котором никто не слушает.
// Кольцо — 32 нулевых байта в base64, только для тестов.
var serviceEnv = []string{"WORKER_MAIL_SMTP_ADDR=127.0.0.1:1", "WORKER_MAIL_FROM=WF <noreply@wf.local>",
	"WORKER_MAIL_SMTP_TLS=none", "WORKER_IDENTITY_SECRET_KEYS=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}

// unreachableDB — адрес базы, на котором никто не слушает: проверка, сработавшая до базы, отличима от ошибки подключения.
const unreachableDB = "WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"

// prepare — Load + Prepare на окружении env (serviceEnv добавляется), журнал — в никуда.
func prepare(t *testing.T, env ...string) (Prepared, error) {
	t.Helper()
	env = append(append([]string{}, serviceEnv...), env...)
	b, err := Load(env, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Prepare(b, env, slog.New(slog.DiscardHandler))
}

// R4: очередь с конкурентностью < 1 — отказ до базы с именем ключа (River отверг бы её в NewClient уже после
// подключения); выключенный импорт свою очередь не проверяет — её нет в карте River.
func TestPrepareRejectsZeroQueues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kv, key string }{
		{"WORKER_QUEUE_MAIL=0", "WORKER_QUEUE_MAIL"},
		{"WORKER_QUEUE_EVENTS=-1", "WORKER_QUEUE_EVENTS"},
		{"WORKER_QUEUE_LIFECYCLE=0", "WORKER_QUEUE_LIFECYCLE"},
		{"WORKER_QUEUE_NOTIFY=0", "WORKER_QUEUE_NOTIFY"},
		{"WORKER_QUEUE_STATS=0", "WORKER_QUEUE_STATS"},
		{"WORKER_QUEUE_MEDIA=0", "WORKER_QUEUE_MEDIA"},
		{"WORKER_QUEUE_MAINTENANCE=0", "WORKER_QUEUE_MAINTENANCE"},
		{"WORKER_QUEUE_IMPORT=0", "WORKER_QUEUE_IMPORT"},
	} {
		t.Run(tc.kv, func(t *testing.T) {
			t.Parallel()
			if _, err := prepare(t, unreachableDB, "WORKER_GEO_IMPORT_ENABLED=true", tc.kv); err == nil ||
				!strings.Contains(err.Error(), tc.key) {
				t.Fatalf("err = %v, ждали ошибку с %s", err, tc.key)
			}
		})
	}
	if _, err := prepare(t, unreachableDB, "WORKER_GEO_IMPORT_ENABLED=false", "WORKER_QUEUE_IMPORT=0"); err != nil {
		t.Fatalf("импорт выключен, очередь import 0: %v", err)
	}
}

// openPool — пул владельца клона: проверки теста и сам Run (роль в тесте не важна — ему нужен живой пул).
func openPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func countJobs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// R11: ctx отменён до Start (здесь — до вызова) — Run возвращает nil, а не обёрнутую context.Canceled ожидания схемы;
// River не стартовал: задач нет.
func TestRunCancelledBeforeStart(t *testing.T) {
	t.Parallel()
	url := dbtest.NewURL(t)
	p, err := prepare(t, "WORKER_DATABASE_URL="+url)
	if err != nil {
		t.Fatal(err)
	}
	pool := openPool(t, url)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, p, pool, slog.New(slog.DiscardHandler), ServiceStop); err != nil {
		t.Fatalf("Run на отменённом ctx: %v", err)
	}
	if n := countJobs(t, pool); n != 0 {
		t.Fatalf("River стартовал на отменённом ctx: задач %d", n)
	}
}

// Спека §8.2: импорт выключен — задача geo.import ждёт в available, пока River работает (периодика его уже
// выполнилась, производители очередей зарегистрированы в river_queue); очереди import среди них нет. Источник
// недостижим — взятая по ошибке задача ушла бы в retryable, а не осталась available. Строка river_queue — прямой
// признак: производитель очереди пишет её на старте, задача же могла бы просто не успеть попасть в выборку.
func TestGeoImportDisabledJobWaits(t *testing.T) {
	t.Parallel()
	url := dbtest.NewURL(t)
	pool := openPool(t, url)
	p, err := prepare(t, "WORKER_DATABASE_URL="+url, "WORKER_GEO_IMPORT_ENABLED=false",
		"WORKER_GEONAMES_BASE_URL=https://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	r, err := ins.Insert(context.Background(), geojobs.NewImportArgs("RU", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, p, pool, log, ServiceStop) }()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM river_job
			WHERE kind = 'ratelimit.cleanup' AND state = 'completed') * (SELECT count(*) FROM river_queue
			WHERE name = 'maintenance')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("периодика воркера не выполнилась за 60 с")
		}
		time.Sleep(100 * time.Millisecond)
	}
	var served int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river_queue WHERE name = $1`, queue.Import).
		Scan(&served); err != nil {
		t.Fatal(err)
	}
	if served != 0 {
		t.Fatalf("импорт выключен, а очередь %s обслуживается", queue.Import)
	}
	var state, q string
	if err := pool.QueryRow(context.Background(), `SELECT state, queue FROM river_job WHERE id = $1`, r.Job.ID).
		Scan(&state, &q); err != nil {
		t.Fatal(err)
	}
	if state != "available" || q != queue.Import {
		t.Fatalf("задача импорта: %s в очереди %s, ждали available в %s", state, q, queue.Import)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("остановка: %v", err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("Run не остановился")
	}
}
