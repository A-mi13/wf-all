package jobs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/staff/internal/app"
	"wf/backend/internal/staff/jobs"
)

// approval — строка approvals: pending со сроком expires или решённая системой (expired) в decided.
func (e *env) approval(expires, decided time.Time) uuid.UUID {
	e.t.Helper()
	aid := id.New()
	status, label := "pending", any(nil)
	var dec any
	if !decided.IsZero() {
		status, label, dec = "expired", "system", decided
	}
	e.exec(`INSERT INTO approvals (id, action, permission, payload, requested_by, requested_by_label, requested_at,
		expires_at, status, decided_by_label, decided_at)
		VALUES ($1, 'staff.invite', 'staff.roles.grant', '{"v": 1}', $2, 'Анна В.', $3, $4, $5::approval_status, $6, $7)`,
		aid, id.New(), expires.Add(-24*time.Hour), expires, status, label, dec)
	return aid
}

// Один проход (P27): чистка staff (сессии и приглашения старше 30 суток), просрочка подтверждений
// (ExpireDue) и удаление решённых старше 180 суток (PurgeDecided с DecidedRetention). Свежее не трогает.
func TestCleanerTick(t *testing.T) {
	e := newEnv(t)
	m := e.member("a@wf.example", "admin", "active", "ru")
	e.clock.Advance(400 * 24 * time.Hour)
	now := e.clock.Now()
	oldSession, liveSession := id.New(), id.New()
	for _, s := range []struct {
		id      uuid.UUID
		expires time.Time
		hash    string
	}{{oldSession, now.Add(-app.CleanupRetention - time.Hour), "aa"}, {liveSession, now.Add(time.Hour), "bb"}} {
		e.exec(`INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, expires_at, ip, user_agent)
			VALUES ($1, $2, decode(repeat($4, 32), 'hex'), 'mfa_pending', $3, $3, $3, '203.0.113.10', 'UA')`,
			s.id, m, s.expires, s.hash)
	}
	due := e.approval(now.Add(-time.Minute), time.Time{})
	pending := e.approval(now.Add(time.Hour), time.Time{})
	purged := e.approval(now.Add(-approvals.DecidedRetention-48*time.Hour), now.Add(-approvals.DecidedRetention-time.Hour))
	kept := e.approval(now.Add(-approvals.DecidedRetention+48*time.Hour), now.Add(-approvals.DecidedRetention+time.Hour))
	if err := jobs.NewCleaner(e.deps, e.svc).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1`, oldSession) != 0 ||
		e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1`, liveSession) != 1 {
		t.Error("сессии: старая не удалена или живая удалена")
	}
	status := func(aid uuid.UUID) string {
		var s string
		if err := e.owner.QueryRow(context.Background(), `SELECT coalesce((SELECT status::text FROM approvals WHERE id = $1), 'нет')`, aid).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, c := range []struct {
		name string
		id   uuid.UUID
		want string
	}{{"просроченный", due, "expired"}, {"ожидающий", pending, "pending"}, {"решённый > 180 суток", purged, "нет"}, {"решённый < 180 суток", kept, "expired"}} {
		if got := status(c.id); got != c.want {
			t.Errorf("%s: %s, ждали %s", c.name, got, c.want)
		}
	}
}

// I-1 финального ревью: ключи идемпотентности admin-api (staff_idempotency_keys) чистит этот тикер — у воркера
// прав на таблицу нет. Просроченный ключ удаляется, живой остаётся; ключи игроков (idempotency_keys) — не наши,
// их чистит воркер. Итог — в строке журнала.
func TestCleanerTickStaffIdempotencyKeys(t *testing.T) {
	e := newEnv(t)
	staffKey := func(key, expires string) {
		e.exec(`INSERT INTO staff_idempotency_keys (user_id, key, endpoint, request_hash, expires_at)
			VALUES ($1, $2, 'PUT /v1/staff/accounts/{staffId}/role', 'h', now() + $3::interval)`, id.New(), key, expires)
	}
	staffKey("old", "-1 minute")
	staffKey("live", "1 hour")
	e.exec(`INSERT INTO idempotency_keys (user_id, key, endpoint, request_hash, expires_at)
		VALUES ($1, 'player', 'POST /v1/teams', 'h', now() - interval '1 minute')`, id.New())
	if err := jobs.NewCleaner(e.deps, e.svc).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM staff_idempotency_keys WHERE key = 'old'`); n != 0 {
		t.Error("просроченный ключ admin-api не удалён")
	}
	if n := e.count(`SELECT count(*) FROM staff_idempotency_keys WHERE key = 'live'`); n != 1 {
		t.Error("живой ключ admin-api удалён")
	}
	if n := e.count(`SELECT count(*) FROM idempotency_keys WHERE key = 'player'`); n != 1 {
		t.Error("ключ игрока удалён тикером admin-api")
	}
	if out := e.log.String(); !strings.Contains(out, "idempotency_keys=1") {
		t.Errorf("в журнале нет числа удалённых ключей:\n%s", out)
	}
}

// Run: проход сразу при старте, затем на каждый тик; ошибка прохода — в журнал сервера, тикер живёт
// дальше (сервис не падает); отмена ctx — Run возвращается.
func TestCleanerRunLogsAndContinues(t *testing.T) {
	e := newEnv(t)
	c := jobs.NewCleaner(e.deps, e.svc)
	passes := make(chan int, 10)
	n := 0
	tick := make(chan time.Time)
	jobs.SetCleanerTestHooks(c, func(context.Context) error {
		n++
		passes <- n
		if n == 1 {
			return errors.New("approvals: просрочка: база недоступна")
		}
		return nil
	}, tick)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	if got := recv(t, passes); got != 1 {
		t.Fatalf("первый проход %d", got)
	}
	send(t, tick) // после неудачного прохода тикер жив
	if got := recv(t, passes); got != 2 {
		t.Fatalf("второй проход %d", got)
	}
	send(t, tick)
	if got := recv(t, passes); got != 3 {
		t.Fatalf("третий проход %d", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run не вернулся после отмены")
	}
	if log := e.log.String(); !strings.Contains(log, "база недоступна") || !strings.Contains(log, "level=ERROR") {
		t.Fatalf("ошибка прохода не в журнале:\n%s", log)
	}
}

// Отмена ctx во время прохода (SIGTERM): проход прерывается, Run возвращается без записи ошибки в
// журнал (остановка — не сбой).
func TestCleanerRunStopsOnCancel(t *testing.T) {
	e := newEnv(t)
	c := jobs.NewCleaner(e.deps, e.svc)
	inPass := make(chan struct{})
	jobs.SetCleanerTestHooks(c, func(ctx context.Context) error {
		close(inPass)
		<-ctx.Done()
		return ctx.Err()
	}, make(chan time.Time))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	recv(t, inPass)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run не вернулся после отмены посреди прохода")
	}
	if strings.Contains(e.log.String(), "level=ERROR") {
		t.Fatalf("остановка записана как сбой:\n%s", e.log.String())
	}
}

// recv — значение из канала теста; не пришло за 10 с — падение, а не зависание до таймаута пакета.
func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("не дождались за 10 с")
		var zero T
		return zero
	}
}

// send — тик Run; Run не принял его за 10 с — падение.
func send(t *testing.T, ch chan<- time.Time) {
	t.Helper()
	select {
	case ch <- time.Now():
	case <-time.After(10 * time.Second):
		t.Fatal("Run не ждёт тика")
	}
}
