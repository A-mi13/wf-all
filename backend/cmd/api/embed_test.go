package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/migrate"
	"wf/backend/internal/platform/testkit/dbtest"
)

// unreachableAPI — база api, на которой никто не слушает: проверка до базы отличима от ошибки подключения.
const unreachableAPI = "API_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1"

// workerEnvNoDB — окружение встроенного воркера без базы: почта на адресе, где никто не слушает (TLS none — петля),
// кольцо identity — testSeed, как у api.
var workerEnvNoDB = []string{"WORKER_MAIL_SMTP_ADDR=127.0.0.1:1", "WORKER_MAIL_FROM=WF <noreply@wf.local>",
	"WORKER_MAIL_SMTP_TLS=none", "WORKER_IDENTITY_SECRET_KEYS=" + testSeed}

// apiBase — обязательное окружение api (без базы и адреса).
func apiBase() []string {
	return append([]string{"API_JWT_SEEDS=" + testSeed, "API_HUMANCHECK_KEYS=" + testSeed}, identityEnv...)
}

// without — env без ключа key.
func without(env []string, key string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return strings.HasPrefix(kv, key+"=") })
}

// embedEnv — один клон базы с правами ролей прода: api работает под ролью api, встроенный воркер — под worker
// (SET ROLE через options строки подключения владельца), порт — свободный.
type embedEnv struct {
	t                 *testing.T
	owner             *pgxpool.Pool
	apiURL, workerURL string
	addr              string
	shutdown          time.Duration // API_SHUTDOWN_TIMEOUT теста
	log               *syncBuffer
	cancel            context.CancelFunc
	done              chan error
}

func newEmbedEnv(t *testing.T) *embedEnv {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api") // права ролей применены к базе теста
	return &embedEnv{t: t, owner: pools.Owner, apiURL: asRole(t, pools.Owner, "api"),
		workerURL: asRole(t, pools.Owner, "worker"), addr: freeAddr(t), log: &syncBuffer{}, shutdown: 2 * time.Second}
}

// asRole — строка подключения владельца, которая сразу делает SET ROLE role.
func asRole(t *testing.T, owner *pgxpool.Pool, role string) string {
	t.Helper()
	u, err := url.Parse(owner.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("options", "-c role="+role)
	// пробел — %20: pgx не читает «+» как пробел
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	return u.String()
}

// environ — api со встроенным воркером; extra — последними (перекрывают).
func (e *embedEnv) environ(extra ...string) []string {
	env := append(apiBase(), "API_HTTP_ADDR="+e.addr, "API_DATABASE_URL="+e.apiURL, "API_LOG_FORMAT=json",
		"API_SHUTDOWN_TIMEOUT="+e.shutdown.String(), "API_EMBED_WORKER=true", "WORKER_DATABASE_URL="+e.workerURL)
	env = append(env, workerEnvNoDB...)
	return append(env, extra...)
}

// start — run в горутине до отмены ctx теста (t.Cleanup отменяет и ждёт).
func (e *embedEnv) start(extra ...string) {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.done = make(chan error, 1)
	env := e.environ(extra...)
	go func() { e.done <- run(ctx, env, e.log) }()
	e.t.Cleanup(func() {
		e.cancel()
		select {
		case err := <-e.done:
			_ = err // исход проверяет сам тест (stop); уборка только дожидается возврата
		case <-time.After(60 * time.Second):
		}
	})
}

// stop — отмена и результат run за limit.
func (e *embedEnv) stop(limit time.Duration) error {
	e.t.Helper()
	e.cancel()
	select {
	case err := <-e.done:
		e.done <- err // для уборки t.Cleanup
		return err
	case <-time.After(limit):
		e.t.Fatalf("run не вернулся за %s после отмены", limit)
		return nil
	}
}

// waitFor — опрос cond до срока; cond — запрос к базе владельцем или журнал.
func (e *embedEnv) waitFor(what string, limit time.Duration, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatalf("за %s не дождались: %s; журнал:\n%s", limit, what, e.log.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e *embedEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// healthz — ответ /healthz api (false — не отвечает).
func (e *embedEnv) healthz() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+e.addr+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// behind — убрать последнюю миграцию из goose_db_version: релиз новее схемы.
func (e *embedEnv) behind() {
	e.t.Helper()
	want, err := migrate.Latest()
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.owner.Exec(context.Background(), `DELETE FROM goose_db_version WHERE version_id = $1`, want); err != nil {
		e.t.Fatal(err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// syncBuffer — журнал, который пишет run и читает тест параллельно.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Спека §3: флаг включён — периодика встроенного River исполняется; отмена — run = nil.
func TestEmbeddedRunsPeriodic(t *testing.T) {
	t.Parallel()
	e := newEmbedEnv(t)
	e.start()
	e.waitFor("ratelimit.cleanup completed", 90*time.Second, func() bool {
		return e.count(`SELECT count(*) FROM river_job WHERE kind = 'ratelimit.cleanup' AND state = 'completed'`) > 0
	})
	if err := e.stop(config.APIEmbeddedStopTime(e.shutdown) + 5*time.Second); err != nil {
		t.Fatalf("остановка: %v", err)
	}
}

// R14: фон работает на втором пуле под ролью worker — у роли api на outbox только INSERT, relay на пуле api не
// опубликовал бы событие.
func TestEmbeddedRelayUnderWorkerRole(t *testing.T) {
	t.Parallel()
	e := newEmbedEnv(t)
	if err := db.InTx(context.Background(), e.owner, func(ctx context.Context, _ pgx.Tx) error {
		_, err := events.Publish(ctx, events.Event{Type: "teams.member_joined", SchemaVersion: 1, AggregateType: "team",
			AggregateID: id.New(), AggregateVersion: 1, Payload: map[string]string{}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.start()
	e.waitFor("событие опубликовано relay", 30*time.Second, func() bool {
		return e.count(`SELECT count(*) FROM outbox WHERE published_at IS NULL`) == 0
	})
	if err := e.stop(config.APIEmbeddedStopTime(e.shutdown) + 5*time.Second); err != nil {
		t.Fatalf("остановка: %v", err)
	}
}

// Рескьюер встроенного River (лидер — api) возвращает зависшее письмо admin-api в очередь, а не выбрасывает
// неизвестный вид: заглушки видов admin-api — те же, что у отдельного воркера.
func TestEmbeddedRescuerKeepsAdminJobs(t *testing.T) {
	t.Parallel()
	e := newEmbedEnv(t)
	var jobID int64
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO river_job (kind, queue, state, args, attempt, max_attempts,
		attempted_at, scheduled_at, priority, metadata)
		VALUES ('staff.mail', 'staff', 'running', '{"v":1,"kind":"staff.invite"}', 1, 10, now() - interval '2 hours',
		now() - interval '2 hours', 1, '{}') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	e.start()
	var state string
	e.waitFor("staff.mail возвращена в очередь", 90*time.Second, func() bool {
		if err := e.owner.QueryRow(context.Background(), `SELECT state FROM river_job WHERE id = $1`, jobID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "discarded" {
			t.Fatalf("задача %d выброшена рескьюером встроенного воркера", jobID)
		}
		return state == "retryable" || state == "available"
	})
	if err := e.stop(config.APIEmbeddedStopTime(e.shutdown) + 5*time.Second); err != nil {
		t.Fatalf("остановка: %v", err)
	}
}

// Флаг не задан — путь прежний: WORKER_* не читаются (мусор в них не мешает), ошибка — о базе api.
func TestEmbedFlagOffIgnoresWorkerEnv(t *testing.T) {
	t.Parallel()
	env := append(apiBase(), "API_HTTP_ADDR=127.0.0.1:0", unreachableAPI, "WORKER_RELAY_BATCH=0", "WORKER_QUEUE_MAIL=0")
	err := run(context.Background(), env, io.Discard)
	if err == nil || strings.Contains(err.Error(), "WORKER_") || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("err = %v, ждали ошибку подключения к базе api без WORKER_", err)
	}
}

// Флаг включён — окружение воркера обязательно до базы: ошибка называет недостающий ключ.
func TestEmbedRequiresWorkerEnv(t *testing.T) {
	t.Parallel()
	full := append(append(apiBase(), "API_HTTP_ADDR=127.0.0.1:0", unreachableAPI, "API_EMBED_WORKER=true",
		"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"), workerEnvNoDB...)
	for _, key := range []string{"WORKER_DATABASE_URL", "WORKER_MAIL_SMTP_ADDR", "WORKER_IDENTITY_SECRET_KEYS"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			if err := run(context.Background(), without(full, key), io.Discard); err == nil ||
				!strings.Contains(err.Error(), key) {
				t.Fatalf("err = %v, ждали ошибку с %s", err, key)
			}
		})
	}
}

// R6: кольцо identity воркера — то же, что у api (сверка по именам ключей по порядку, до базы): другое кольцо или
// другой порядок — отказ с обоими именами ключей и без значений; тот же ключ в base64 и base64url — одно кольцо.
func TestEmbedRejectsDifferentRing(t *testing.T) {
	t.Parallel()
	raw := bytes.Repeat([]byte{0xFB, 0xFF}, 16)
	std, urlKey := base64.StdEncoding.EncodeToString(raw), base64.URLEncoding.EncodeToString(raw)
	if std == urlKey || !strings.ContainsAny(std, "+/") {
		t.Fatalf("записи ключа совпадают (%q) — случай вакуумен", std)
	}
	other := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	envWith := func(apiKeys, workerKeys string) []string {
		env := append(apiBase(), "API_HTTP_ADDR=127.0.0.1:0", unreachableAPI, "API_EMBED_WORKER=true",
			"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1")
		env = append(without(env, "API_IDENTITY_SECRET_KEYS"), "API_IDENTITY_SECRET_KEYS="+apiKeys)
		return append(without(workerEnvNoDB, "WORKER_IDENTITY_SECRET_KEYS"), append(env,
			"WORKER_IDENTITY_SECRET_KEYS="+workerKeys)...)
	}
	for name, tc := range map[string]struct{ api, worker string }{
		"другой ключ":      {std, other},
		"другой порядок":   {std + "," + other, other + "," + std},
		"лишний у воркера": {std, std + "," + other},
	} {
		err := run(context.Background(), envWith(tc.api, tc.worker), io.Discard)
		if err == nil || !strings.Contains(err.Error(), "WORKER_IDENTITY_SECRET_KEYS") ||
			!strings.Contains(err.Error(), "API_IDENTITY_SECRET_KEYS") {
			t.Errorf("%s: err = %v, ждали отказ с обоими ключами", name, err)
			continue
		}
		for _, secret := range []string{std, urlKey, other} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: значение ключа в ошибке: %v", name, err)
			}
		}
	}
	// тот же ключ в другой записи — сверка пройдена, дальше — подключение к недостижимой базе
	err := run(context.Background(), envWith(std, urlKey), io.Discard)
	if err == nil || strings.Contains(err.Error(), "SECRET_KEYS") || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("base64 и base64url одного ключа: err = %v, ждали ошибку подключения", err)
	}
}

// R5: остановка встроенного режима по умолчанию укладывается в бюджет и складывается из сроков embeddedStop.
func TestEmbeddedShutdownBudget(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load[config.API]("API_", append(apiBase(), "API_HTTP_ADDR=:0", unreachableAPI))
	if err != nil {
		t.Fatal(err)
	}
	drain := cfg.HTTP.ShutdownTimeout
	got := config.APIEmbeddedStopTime(drain)
	if want := drain + embeddedStop().Relay + embeddedStop().Soft + config.HardStop; got != want || got > config.StopBudget {
		t.Fatalf("остановка встроенного режима %s (ждали %s = дренаж %s + relay %s + River %s + %s), бюджет %s",
			got, want, drain, embeddedStop().Relay, embeddedStop().Soft, config.HardStop, config.StopBudget)
	}
}

// Отказ фона (схема отстала дольше WORKER_DB_SCHEMA_WAIT) гасит HTTP: run возвращается сам, без отмены ctx, с
// ошибкой фона; порт после этого закрыт.
func TestEmbeddedBackgroundFailureStopsHTTP(t *testing.T) {
	t.Parallel()
	e := newEmbedEnv(t)
	e.behind()
	e.start("WORKER_DB_SCHEMA_WAIT=1s")
	select {
	case err := <-e.done:
		e.done <- err
		if !errors.Is(err, migrate.ErrSchemaBehind) || !strings.Contains(err.Error(), "фон") {
			t.Fatalf("err = %v, ждали ErrSchemaBehind с префиксом «фон»", err)
		}
	case <-time.After(time.Second + e.shutdown + 10*time.Second):
		t.Fatal("отказ фона не остановил api")
	}
	if e.healthz() {
		t.Fatal("api отвечает после отказа фона")
	}
}

// SIGTERM, пока встроенный воркер ждёт схему, — штатная остановка: run = nil в пределах срока остановки.
func TestEmbeddedStopDuringSchemaWait(t *testing.T) {
	t.Parallel()
	e := newEmbedEnv(t)
	e.behind()
	e.start("WORKER_DB_SCHEMA_WAIT=10m")
	e.waitFor("/healthz 200", 30*time.Second, e.healthz)
	e.waitFor("воркер ждёт схему", 30*time.Second, func() bool { return strings.Contains(e.log.String(), "отстаёт") })
	if err := e.stop(config.APIEmbeddedStopTime(e.shutdown)); err != nil {
		t.Fatalf("остановка во время ожидания схемы: %v", err)
	}
}

// Журнал встроенного воркера — журнал api (service=api) с component=worker.
func TestEmbeddedLogComponent(t *testing.T) {
	t.Parallel()
	e := newEmbedEnv(t)
	e.start()
	e.waitFor("строка «воркер запущен»", 60*time.Second, func() bool {
		for line := range strings.SplitSeq(e.log.String(), "\n") {
			if strings.Contains(line, "воркер запущен") && strings.Contains(line, `"component":"worker"`) &&
				strings.Contains(line, `"service":"api"`) {
				return true
			}
		}
		return false
	})
	if err := e.stop(config.APIEmbeddedStopTime(e.shutdown) + 5*time.Second); err != nil {
		t.Fatalf("остановка: %v", err)
	}
}

// Ревью n1: SIGTERM, пока открывается второй пул (холодный старт Neon, ping — секунды), — штатная остановка: run = nil,
// а не «db: ping: context canceled» с кодом 1. Без отмены та же недостижимая база — ошибка подключения.
func TestEmbedStopDuringWorkerDBOpen(t *testing.T) {
	t.Parallel()
	env := append(append(apiBase(), "API_HTTP_ADDR=127.0.0.1:0", unreachableAPI, "API_EMBED_WORKER=true",
		"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf?connect_timeout=1"), workerEnvNoDB...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, env, io.Discard); err != nil {
		t.Fatalf("отмена во время открытия пула воркера: %v", err)
	}
	if err := run(context.Background(), env, io.Discard); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("без отмены err = %v, ждали ошибку подключения", err)
	}
}
