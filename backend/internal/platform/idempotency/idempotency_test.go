package idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/testkit/dbtest"
)

var errBusiness = errors.New("бизнес-ошибка")

// probe — ручка «создать»: пишет строку в своей транзакции и отвечает 201 с Location.
type probe struct {
	pool    *pgxpool.Pool
	log     *slog.Logger
	calls   atomic.Int32
	fail    atomic.Bool   // бизнес-ошибка 422 в транзакции
	noTx    bool          // успех без транзакции — нарушение §6.4
	twoTx   bool          // две транзакции в одном запросе
	entered chan struct{} // не nil — сигнал «ключ вставлен, транзакция открыта»
	gate    chan struct{} // не nil — транзакция ждёт закрытия
}

func (p *probe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.calls.Add(1)
	if p.noTx {
		w.WriteHeader(http.StatusCreated)
		return
	}
	work := func(ctx context.Context, tx pgx.Tx) error {
		if p.entered != nil {
			p.entered <- struct{}{}
			<-p.gate
		}
		if _, err := tx.Exec(ctx, "INSERT INTO feature_flags (key) VALUES ($1)", "probe-"+uuid.NewString()); err != nil {
			return err
		}
		if p.fail.Load() {
			return errBusiness
		}
		return nil
	}
	err := db.InTx(r.Context(), p.pool, work)
	if err == nil && p.twoTx {
		err = db.InTx(r.Context(), p.pool, work)
	}
	switch {
	case errors.Is(err, errBusiness):
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	case err != nil:
		httpx.WriteError(p.log, w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/things/1")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id": 1, "name": "x"}`))
}

type env struct {
	pools dbtest.Pools
	probe *probe
	h     http.Handler
	logs  *bytes.Buffer
	user  uuid.UUID
}

func setup(t *testing.T) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api")
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	p := &probe{pool: pools.As, log: log}
	return &env{pools: pools, probe: p, logs: logs, user: uuid.New(),
		h: idempotency.Middleware(pools.As, idempotency.Config{LockTimeout: 200 * time.Millisecond}, log)(p)}
}

func (e *env) do(method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set(idempotency.Header, key)
	}
	r = r.WithContext(auth.With(r.Context(), &auth.Principal{UserID: e.user}))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *env) rows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pools.Owner.QueryRow(context.Background(), "SELECT count(*) FROM feature_flags WHERE key LIKE 'probe-%'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("не Problem: %s", rec.Body.String())
	}
	return p.Code
}

// без энтропии — gitleaks не примет за секрет
var key = strings.Repeat("k", 20)

func TestReplaySameRequest(t *testing.T) {
	e := setup(t)
	first := e.do(http.MethodPost, "/v1/things?x=1", key, `{"name":"x"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("первый: %d %s", first.Code, first.Body.String())
	}
	again := e.do(http.MethodPost, "/v1/things?x=1", key, `{"name":"x"}`)
	if again.Code != http.StatusCreated || again.Header().Get("Location") != "/v1/things/1" ||
		again.Header().Get("Content-Type") != "application/json" || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор: %d %v", again.Code, again.Header())
	}
	var a, b map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(again.Body.Bytes(), &b)
	if a["id"] != b["id"] || a["name"] != b["name"] {
		t.Fatalf("тело повтора: %s", again.Body.String())
	}
	if e.probe.calls.Load() != 1 || e.rows(t) != 1 {
		t.Fatalf("исполнено %d раз, строк %d", e.probe.calls.Load(), e.rows(t))
	}
}

func TestKeyReusedWithOtherRequest(t *testing.T) {
	e := setup(t)
	e.do(http.MethodPost, "/v1/things", key, `{"name":"x"}`)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"другое тело":  e.do(http.MethodPost, "/v1/things", key, `{"name":"y"}`),
		"другой путь":  e.do(http.MethodPost, "/v1/teams", key, `{"name":"x"}`),
		"другой query": e.do(http.MethodPost, "/v1/things?a=1", key, `{"name":"x"}`),
	} {
		if rec.Code != http.StatusUnprocessableEntity || code(t, rec) != httpx.CodeIdempotencyKeyReused {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if e.probe.calls.Load() != 1 {
		t.Fatalf("исполнено %d раз", e.probe.calls.Load())
	}
}

// Бизнес-ошибка откатывает ключ вместе с транзакцией — повтор исполняется заново (§6.4).
func TestBusinessErrorRollsBackKey(t *testing.T) {
	e := setup(t)
	e.probe.fail.Store(true)
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("первый: %d", rec.Code)
	}
	e.probe.fail.Store(false)
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("повтор после 4xx не исполнен заново: %d", rec.Code)
	}
	if e.probe.calls.Load() != 2 || e.rows(t) != 1 {
		t.Fatalf("исполнено %d, строк %d", e.probe.calls.Load(), e.rows(t))
	}
}

// Review Focus 2: дубль, пока первый в транзакции, — 409 in_progress; после коммита — повтор.
func TestParallelDuplicate(t *testing.T) {
	e := setup(t)
	e.probe.entered, e.probe.gate = make(chan struct{}, 2), make(chan struct{})
	var first *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Go(func() { first = e.do(http.MethodPost, "/v1/things", key, `{}`) })
	<-e.probe.entered // первый вставил ключ и держит транзакцию

	start := time.Now()
	dup := e.do(http.MethodPost, "/v1/things", key, `{}`)
	if dup.Code != http.StatusConflict || code(t, dup) != httpx.CodeIdempotencyInProgress {
		t.Fatalf("дубль: %d %s", dup.Code, dup.Body.String())
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("дубль ждал %v — lock_timeout не сработал", waited)
	}
	close(e.probe.gate)
	wg.Wait()
	if first.Code != http.StatusCreated {
		t.Fatalf("первый: %d %s", first.Code, first.Body.String())
	}
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("после коммита: %d", rec.Code)
	}
	if e.rows(t) != 1 {
		t.Fatalf("строк %d — действие исполнено дважды", e.rows(t))
	}
}

// Закоммичено, а ответ не сохранился (обрыв между коммитом и записью ответа).
func TestReplayUnavailable(t *testing.T) {
	e := setup(t)
	hash := idempotency.RequestHash(http.MethodPost, "/v1/things", []byte(`{}`))
	if _, err := e.pools.Owner.Exec(context.Background(),
		`INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash) VALUES ($1, $2, '/v1/things', $3)`,
		e.user, key, hash); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodPost, "/v1/things", key, `{}`)
	if rec.Code != http.StatusConflict || code(t, rec) != httpx.CodeIdempotencyReplayUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.probe.calls.Load() != 0 {
		t.Fatal("действие исполнено повторно")
	}
}

// Просроченный ключ (24 ч) переиспользуется.
func TestExpiredKeyReclaimed(t *testing.T) {
	e := setup(t)
	if _, err := e.pools.Owner.Exec(context.Background(),
		`INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash, response_status, expires_at)
		 VALUES ($1, $2, '/old', 'other', 200, now() - interval '1 second')`, e.user, key); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("новый ответ не сохранён")
	}
}

// Ручка, требующая идемпотентности, обязана выполняться в одной транзакции (§6.4).
func TestHandlerContractViolations(t *testing.T) {
	t.Run("успех без транзакции — 500", func(t *testing.T) {
		e := setup(t)
		e.probe.noTx = true
		rec := e.do(http.MethodPost, "/v1/things", key, `{}`)
		if rec.Code != http.StatusInternalServerError || code(t, rec) != httpx.CodeInternal {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(e.logs.String(), "без транзакции") {
			t.Fatalf("нарушение не в логе: %s", e.logs.String())
		}
	})
	t.Run("вторая транзакция — ошибка", func(t *testing.T) {
		e := setup(t)
		e.probe.twoTx = true
		if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d", rec.Code)
		}
		if e.rows(t) != 1 {
			t.Fatalf("строк %d — вторая транзакция закоммитилась", e.rows(t))
		}
	})
}

func TestPassthrough(t *testing.T) {
	e := setup(t)
	// GET, без ключа, без пользователя — механизм не включается
	if rec := e.do(http.MethodGet, "/v1/things", key, ""); rec.Code != http.StatusCreated {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/v1/things", "", `{}`); rec.Code != http.StatusCreated {
		t.Fatalf("без ключа: %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/things", strings.NewReader(`{}`))
	r.Header.Set(idempotency.Header, key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("без пользователя: %d", rec.Code)
	}
	var n int
	_ = e.pools.Owner.QueryRow(context.Background(), "SELECT count(*) FROM idempotency_keys").Scan(&n)
	if n != 0 {
		t.Fatalf("ключей %d", n)
	}
}
