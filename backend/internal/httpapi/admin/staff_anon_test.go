package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

// anonPaths — анонимные мутации админки (x-anonymous-mutations).
var anonPaths = []string{pathLogin, pathAccept, pathResend}

// anonKeyRows — строки ключей идемпотентности анонимных операций в обеих таблицах: ключи admin-api — в
// staff_idempotency_keys (I-1), idempotency_keys — на случай, если конвейер собран не с той таблицей.
const anonKeyRows = `SELECT (SELECT count(*) FROM staff_idempotency_keys WHERE endpoint = ANY($1))
	+ (SELECT count(*) FROM idempotency_keys WHERE endpoint = ANY($1))`

// Анонимные операции (x-permission anonymous, security: []) не идут под идемпотентностью даже с живой
// cookie и присланным Idempotency-Key (hard rule 5, ревью 10a I1): самоотправка — 202 и на почту без
// приглашения, и на подходящую (иначе 500 «успех без транзакции» выдавал бы, есть ли приглашение);
// вход и принятие с ключом и cookie не оставляют строк ключей (ни в staff_idempotency_keys, ни в idempotency_keys). Сотрудник старой cookie в
// ctx остаётся — D9 у входа работает.
func TestStaffAnonymousNoIdempotency(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	e.invite("new@wf.example", string(perm.Support))
	e.clock.Advance(61 * time.Second)
	for _, email := range []string{"nobody@wf.example", "new@wf.example"} {
		rec := e.send(http.MethodPost, pathResend, m.token, uuid.NewString(), jsonOf(t, map[string]string{"email": email}))
		wantStatus(t, rec, http.StatusAccepted)
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail'`); n != 1 {
		t.Fatalf("писем %d", n)
	}
	rec := e.send(http.MethodPost, pathAccept, m.token, uuid.NewString(),
		jsonOf(t, map[string]string{"email": "new@wf.example", "code": inviteCode, "password": staffPassword}))
	wantStatus(t, rec, http.StatusOK)
	newToken(t, rec)
	other := e.signIn(m)
	rec = e.send(http.MethodPost, pathLogin, other, uuid.NewString(), e.creds(m.email, staffPassword))
	wantStatus(t, rec, http.StatusOK)
	newToken(t, rec)
	if r := e.sessionReason(other); r != "replaced" {
		t.Fatalf("D9 у входа с ключом: %q", r)
	}
	if n := e.count(anonKeyRows, anonPaths); n != 0 {
		t.Fatalf("строк идемпотентности у анонимных операций: %d", n)
	}
}

// Ревью 10a I1, сценарий B: принятие с ключом и живой cookie, а код перевыпущен, пока запрос ждал замок
// учётки (сверка без замков прошла, под замком — новый код). Ответ — 400 staff.invite_invalid с записью
// неудачи в журнале (вторая транзакция acceptFailed), а не 500 «вторая транзакция в идемпотентном
// запросе». Барьер — ожидание замка в pg_locks, перевыпуск — в транзакции, держащей замок.
func TestStaffAcceptReissuedWhileWaitingWithKey(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	staffID := e.invite("new@wf.example", string(perm.Support))
	ctx := context.Background()
	hold, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, staffID); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httpRec, 1)
	go func() {
		done <- e.send(http.MethodPost, pathAccept, m.token, uuid.NewString(),
			jsonOf(t, map[string]string{"email": "new@wf.example", "code": inviteCode, "password": staffPassword}))
	}()
	// запрос дошёл до замка учётки: соединение этой базы ждёт замок (pg_locks — весь кластер, а соседние
	// пакеты гоняют тесты с замками на клонах того же кластера; у ожидания строки database в pg_locks пуст)
	deadline := time.Now().Add(30 * time.Second)
	for {
		var waiting int
		if err := e.owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("запрос не дошёл до замка учётки")
		}
		time.Sleep(10 * time.Millisecond)
	}
	newID := uuid.New()
	ct, keyID, err := e.ring.Seal([]byte("aaaaa-bbbbb"), newID[:])
	if err != nil {
		t.Fatal(err)
	}
	now := e.clock.Now()
	if _, err := hold.Exec(ctx, `UPDATE staff_invites SET revoked_at = $2, code_enc = NULL, code_key_id = NULL
		WHERE staff_id = $1 AND revoked_at IS NULL AND consumed_at IS NULL`, staffID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `INSERT INTO staff_invites (id, staff_id, purpose, code_enc, code_key_id, expires_at, sends, last_sent_at, created_at)
		VALUES ($1, $2, 'invite', $3, $4, $5, 1, $6, $6)`, newID, staffID, ct, keyID, now.Add(24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rec := <-done
	wantProblem(t, rec, http.StatusBadRequest, staff.CodeInviteInvalid)
	noCookie(t, rec)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.invite_accept_failed'`); n != 1 {
		t.Fatalf("записей неудачи %d", n)
	}
	if n := e.count(anonKeyRows, anonPaths); n != 0 {
		t.Fatalf("строк идемпотентности %d", n)
	}
}
