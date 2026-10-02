package idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

var (
	errBusiness = errors.New("бизнес-ошибка")
	errRetry    = errors.New("откат как при 40001 — ручка повторяет транзакцию")
)

const (
	headerProbe     = "X-Probe"      // имя запроса в сценариях гонок
	headerProbeFail = "X-Probe-Fail" // не пусто — бизнес-ошибка 422 только у этого запроса
)

// probe — ручка «создать»: пишет строку в своей транзакции и отвечает 201 с Location.
type probe struct {
	pool      *pgxpool.Pool
	log       *slog.Logger
	calls     atomic.Int32
	fail      atomic.Bool   // бизнес-ошибка 422 в транзакции
	retryOnce atomic.Bool   // первая транзакция откатывается (errRetry), ручка исполняет её заново
	noTx      bool          // успех без транзакции — нарушение §6.4
	twoTx     bool          // две транзакции в одном запросе
	entered   chan struct{} // не nil — сигнал «ключ вставлен, транзакция открыта»
	gate      chan struct{} // не nil — транзакция ждёт закрытия
	// lockTimeout — lock_timeout роли: бизнес-запросы транзакции идут с ним, а не с таймаутом ключа
	lockTimeout string
	inTx        func(r *http.Request) // не nil — зовётся в транзакции после вставки ключа
	afterTx     func(r *http.Request) // не nil — зовётся после db.InTx, до ответа
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
		if p.inTx != nil {
			p.inTx(r)
		}
		var lt string
		if err := tx.QueryRow(ctx, "SELECT current_setting('lock_timeout')").Scan(&lt); err != nil {
			return err
		}
		if lt != p.lockTimeout {
			return fmt.Errorf("lock_timeout бизнес-транзакции %q, у роли %q — хук не вернул прежний", lt, p.lockTimeout)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO feature_flags (key) VALUES ($1)", "probe-"+uuid.NewString()); err != nil {
			return err
		}
		if p.retryOnce.CompareAndSwap(true, false) {
			return errRetry
		}
		if p.fail.Load() || r.Header.Get(headerProbeFail) != "" {
			return errBusiness
		}
		return nil
	}
	err := db.InTx(r.Context(), p.pool, work)
	if errors.Is(err, errRetry) {
		err = db.InTx(r.Context(), p.pool, work)
	}
	if err == nil && p.twoTx {
		err = db.InTx(r.Context(), p.pool, work)
	}
	if p.afterTx != nil {
		p.afterTx(r)
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
	return setupWith(t, 200*time.Millisecond)
}

func setupWith(t *testing.T, lockTimeout time.Duration) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api")
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	p := &probe{pool: pools.As, log: log}
	if err := pools.As.QueryRow(context.Background(), "SELECT current_setting('lock_timeout')").Scan(&p.lockTimeout); err != nil {
		t.Fatal(err)
	}
	return &env{pools: pools, probe: p, logs: logs, user: uuid.New(),
		h: idempotency.Middleware(pools.As, idempotency.Config{LockTimeout: lockTimeout}, log)(p)}
}

// header — заголовок запроса для сценариев с несколькими запросами.
func header(k, v string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

func (e *env) do(method, path, key, body string, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set(idempotency.Header, key)
	}
	for _, o := range opts {
		o(r)
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

// waitLockWaiter — какой-то запрос базы встал в ожидание блокировки (дубль ждёт на ключе).
func waitLockWaiter(t *testing.T, owner *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err := owner.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("запрос не встал в ожидание на ключе за 5 с")
		}
		time.Sleep(10 * time.Millisecond)
	}
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
// LockTimeout меньше 1 мс не превращается в "0ms" (ждать без конца).
func TestParallelDuplicate(t *testing.T) {
	for _, lt := range []time.Duration{200 * time.Millisecond, time.Microsecond} {
		t.Run(lt.String(), func(t *testing.T) {
			e := setupWith(t, lt)
			e.probe.entered, e.probe.gate = make(chan struct{}, 2), make(chan struct{})
			var first *httptest.ResponseRecorder
			var wg sync.WaitGroup
			wg.Go(func() { first = e.do(http.MethodPost, "/v1/things", key, `{}`) })
			<-e.probe.entered // первый вставил ключ и держит транзакцию

			start := time.Now()
			dupDone := make(chan *httptest.ResponseRecorder, 1)
			go func() { dupDone <- e.do(http.MethodPost, "/v1/things", key, `{}`) }()
			var dup *httptest.ResponseRecorder
			select {
			case dup = <-dupDone:
			case <-time.After(5 * time.Second):
				close(e.probe.gate) // отпустить первый, чтобы тест не висел
				wg.Wait()
				t.Fatal("дубль ждёт первый дольше 5 с — lock_timeout не сработал")
			}
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
		})
	}
}

// Раунд 1, I1: A вставил ключ, B ждёт на индексе; A откатывается с 422, B занимает ключ и
// коммитится раньше, чем A дошёл до сохранения ответа. Ответ A (откаченный ключ) не должен
// попасть в ключ B — иначе все повторы B получат чужой 422.
func TestRolledBackRequestDoesNotSaveIntoWinnerKey(t *testing.T) {
	e := setupWith(t, 5*time.Second)
	aEntered, aGo := make(chan struct{}), make(chan struct{})
	aRolledBack, aFinish := make(chan struct{}), make(chan struct{})
	bCommitted, bGo := make(chan struct{}), make(chan struct{})
	e.probe.inTx = func(r *http.Request) {
		if r.Header.Get(headerProbe) == "A" {
			aEntered <- struct{}{}
			<-aGo
		}
	}
	e.probe.afterTx = func(r *http.Request) {
		switch r.Header.Get(headerProbe) {
		case "A":
			aRolledBack <- struct{}{}
			<-aFinish
		case "B":
			bCommitted <- struct{}{}
			<-bGo
		}
	}
	aDone, bDone := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() {
		aDone <- e.do(http.MethodPost, "/v1/things", key, `{}`, header(headerProbe, "A"), header(headerProbeFail, "1"))
	}()
	<-aEntered // A держит ключ в открытой транзакции
	go func() { bDone <- e.do(http.MethodPost, "/v1/things", key, `{}`, header(headerProbe, "B")) }()
	waitLockWaiter(t, e.pools.Owner) // B ждёт на уникальном индексе

	close(aGo)
	<-aRolledBack // A откатился и ещё не ответил
	<-bCommitted  // B занял ключ, исполнился и закоммитился, ответ ещё не сохранён
	close(aFinish)
	a := <-aDone // A ответил (старый код здесь записал бы свой 422 в ключ B)
	close(bGo)
	b := <-bDone

	if a.Code != http.StatusUnprocessableEntity || b.Code != http.StatusCreated {
		t.Fatalf("A: %d, B: %d %s", a.Code, b.Code, b.Body.String())
	}
	rec := e.do(http.MethodPost, "/v1/things", key, `{}`)
	if rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "true" ||
		rec.Header().Get("Location") != "/v1/things/1" {
		t.Fatalf("повтор получил не ответ B: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if e.rows(t) != 1 {
		t.Fatalf("строк %d", e.rows(t))
	}
}

// Раунд 1, I3: ключ появился между Get и Claim (чужая транзакция закоммитилась, пока Claim
// ждал на индексе): тот же запрос — 409 in_progress, другой — 422 key_reused; действие не
// исполнено.
func TestClaimAfterConcurrentCommit(t *testing.T) {
	same := idempotency.RequestHash(http.MethodPost, "/v1/things", []byte(`{}`))
	for _, tc := range []struct {
		name, hash, code string
		status           int
	}{
		{"тот же запрос", same, httpx.CodeIdempotencyInProgress, http.StatusConflict},
		{"другой запрос", "other", httpx.CodeIdempotencyKeyReused, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setupWith(t, 2*time.Second)
			ctx := context.Background()
			tx, err := e.pools.Owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash)
				VALUES ($1, $2, '/v1/things', $3)`, e.user, key, tc.hash); err != nil {
				t.Fatal(err)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- e.do(http.MethodPost, "/v1/things", key, `{}`) }()
			waitLockWaiter(t, e.pools.Owner) // Get ключа не увидел, Claim ждёт на индексе
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			rec := <-done
			if rec.Code != tc.status || code(t, rec) != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if e.rows(t) != 0 {
				t.Fatalf("строк %d — действие исполнено", e.rows(t))
			}
		})
	}
}

// Откат транзакции снимает ключ: ручка, повторившая транзакцию (40001), вставляет его заново,
// и ответ сохраняется.
func TestRetryAfterRollbackReclaimsKey(t *testing.T) {
	e := setup(t)
	e.probe.retryOnce.Store(true)
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated {
		t.Fatalf("первый: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodPost, "/v1/things", key, `{}`); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор: %d %v", rec.Code, rec.Header())
	}
	if e.probe.calls.Load() != 1 || e.rows(t) != 1 {
		t.Fatalf("исполнено %d, строк %d", e.probe.calls.Load(), e.rows(t))
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
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/things", strings.NewReader(`{}`))
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
