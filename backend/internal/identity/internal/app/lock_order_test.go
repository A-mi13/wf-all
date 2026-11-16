package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
)

// Стражи N1 и R3-3 плана I-2 (Task 20): сценарий, который узнаёт аккаунт по строке (код из письма, refresh), ищет её
// без замка → lockUser → FOR UPDATE строки (lockByRow, R30). Удаление аккаунта берёт users, гасит коды и отзывает
// сессии. Барьер — канал на hooks.afterRowFound(kind): сценарий нашёл строку и стоит; удаление обязано пройти целиком
// (сценарий ничего не держит), затем сценарий продолжает и видит перепроверенную строку. FOR UPDATE строки до lockUser
// поставил бы удаление на строку, а сценарий — на users: удаление не уложится в срок барьера (2 с, не бесконечно), а
// продолжение даст 40P01.

// deleteDuring — op встаёт на afterRowFound(kind); пока стоит, DeleteAccount(sid, nick) идёт до конца (не дольше 2 с),
// затем op продолжается. Уложилось ли удаление в срок, итог op и удаления.
func deleteDuring(t *testing.T, e *env, kind string, sid uuid.UUID, nick string, op func() error) (inTime bool, opErr, delErr error) {
	t.Helper()
	found := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	e.a.hooks = &hooks{afterRowFound: func(k string) {
		if k != kind {
			return
		}
		once.Do(func() {
			close(found)
			<-resume
		})
	}}
	defer func() { e.a.hooks = nil }()
	var wg sync.WaitGroup
	wg.Go(func() { opErr = op() })
	select {
	case <-found:
	case <-time.After(10 * time.Second):
		close(resume)
		wg.Wait()
		t.Fatalf("сценарий не дошёл до afterRowFound(%q): %v", kind, opErr)
	}
	delDone := make(chan struct{})
	go func() {
		defer close(delDone)
		_, delErr = e.a.DeleteAccount(context.Background(), sid, nick)
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-delDone:
		inTime = true
	case <-timer.C:
	}
	close(resume)
	wg.Wait()
	<-delDone
	return inTime, opErr, delErr
}

// confirmEmailChange стоит на найденной строке кода, deleteAccount той же учётки проходит целиком (гасит код);
// подтверждение — 410 code_expired, ни одного 40P01, почта прежняя.
func TestConfirmEmailChangeVsDeleteNoDeadlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "lock_ec_del", "ru")
	sid := issue(t, e, uid, web("x")).SessionID
	sent, err := e.a.StartEmailChange(ctx, sid, "lock_ec_del_new@example.com", web("req"))
	if err != nil {
		t.Fatal(err)
	}
	code := plainCode(t, e, sent.CodeID)
	inTime, opErr, delErr := deleteDuring(t, e, "code", sid, "lock_ec_del", func() error {
		_, err := e.a.ConfirmEmailChange(ctx, uid, sent.CodeID, code)
		return err
	})
	if !inTime || delErr != nil || isDeadlock(delErr) || isDeadlock(opErr) || problemCode(opErr) != identity.CodeCodeExpired {
		t.Fatalf("удаление: в срок %v, %v (40P01: %v); подтверждение: %v (40P01: %v)", inTime, delErr, isDeadlock(delErr),
			opErr, isDeadlock(opErr))
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND email = 'lock_ec_del@example.com'
		AND status = 'pending_deletion'`, uid); n != 1 {
		t.Fatal("почта сменилась или удаление не записано")
	}
}

// confirmPasswordReset (tx1) стоит на найденной строке кода, deleteAccount проходит целиком; сброс — 410
// code_expired, ни одного 40P01, пароль прежний. Барьер — в tx1, а не в tx2: tx1 ищет код без замка (checkCode →
// lockByRow), а tx2 берёт users первой (lockUser, password.go:210) и только потом lockCode (:217) — барьер
// afterRowFound в tx2 стоял бы под замком users, и удаление не уложилось бы по построению, а не из-за ошибки порядка.
// Удаление между tx1 и tx2 (во время Hash) — TestConfirmResetDeleteBetweenTx.
func TestConfirmResetVsDeleteNoDeadlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "lock_rs_del", "ru")
	setPassword(t, e, uid, "h:old")
	sid := issue(t, e, uid, web("x")).SessionID
	sent := startReset(t, e, "lock_rs_del@example.com")
	code := plainCode(t, e, sent.CodeID)
	inTime, opErr, delErr := deleteDuring(t, e, "code", sid, "lock_rs_del", func() error {
		_, err := confirmReset(e, sent, code, nil, resetNewPassword)
		return err
	})
	if !inTime || delErr != nil || isDeadlock(opErr) || problemCode(opErr) != identity.CodeCodeExpired {
		t.Fatalf("удаление: в срок %v, %v; сброс: %v (40P01: %v)", inTime, delErr, opErr, isDeadlock(opErr))
	}
	if h := passwordHash(t, e, uid); h != "h:old" {
		t.Fatalf("пароль сменился: %q", h)
	}
}

// gateHasher — хэшер теста, у которого Hash сигналит entered и ждёт gate (точка между tx1 и tx2 сброса пароля:
// argon2 вне транзакций, R31); Verify — как у обёрнутого.
type gateHasher struct {
	identity.PasswordHasher
	entered chan struct{}
	gate    chan struct{}
}

func (h *gateHasher) Hash(ctx context.Context, pw string) (string, error) {
	close(h.entered)
	select {
	case <-h.gate:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return h.PasswordHasher.Hash(ctx, pw)
}

// Удаление аккаунта между tx1 (код верен, закоммичено) и tx2 сброса — пока идёт Hash: сброс ничего не держит,
// удаление проходит целиком (гасит коды); tx2 перепроверяет код под замком — 410 code_expired, пароль прежний,
// сессий от сброса нет.
func TestConfirmResetDeleteBetweenTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "lock_rs_btw", "ru")
	setPassword(t, e, uid, "h:old")
	sid := issue(t, e, uid, web("x")).SessionID
	sent := startReset(t, e, "lock_rs_btw@example.com")
	code := plainCode(t, e, sent.CodeID)
	h := &gateHasher{PasswordHasher: e.a.hasher, entered: make(chan struct{}), gate: make(chan struct{})}
	e.a.hasher = h
	var opErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		_, opErr = confirmReset(e, sent, code, nil, resetNewPassword)
	})
	select {
	case <-h.entered:
	case <-time.After(10 * time.Second):
		close(h.gate)
		wg.Wait()
		t.Fatalf("сброс не дошёл до Hash: %v", opErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, delErr := e.a.DeleteAccount(ctx, sid, "lock_rs_btw")
	cancel()
	close(h.gate)
	wg.Wait()
	if delErr != nil || isDeadlock(opErr) || problemCode(opErr) != identity.CodeCodeExpired {
		t.Fatalf("удаление: %v; сброс: %v (40P01: %v)", delErr, opErr, isDeadlock(opErr))
	}
	if ph := passwordHash(t, e, uid); ph != "h:old" {
		t.Fatalf("пароль сменился: %q", ph)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 0 {
		t.Fatalf("живых сессий после удаления: %d", n)
	}
}

// R3-3: повтор использованного refresh стоит на найденной строке токена, deleteAccount проходит целиком (отзывает
// сессии); refresh — 401 refresh_invalid, ни одного 40P01.
func TestRefreshReuseVsDeleteNoDeadlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	_, is := loggedIn(t, e, "lock_rf_del", iphone)
	if _, err := e.a.Refresh(ctx, is.Tokens.Refresh, installID, iphone); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute) // вне окна гонки 15 с — повтор использованного
	inTime, opErr, delErr := deleteDuring(t, e, "refresh", is.SessionID, "lock_rf_del", func() error {
		_, err := e.a.Refresh(ctx, is.Tokens.Refresh, installID, iphone)
		return err
	})
	if !inTime || delErr != nil || isDeadlock(opErr) || problemCode(opErr) != identity.CodeRefreshInvalid {
		t.Fatalf("удаление: в срок %v, %v; refresh: %v (40P01: %v)", inTime, delErr, opErr, isDeadlock(opErr))
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_reason = 'account_deleted'`, is.SessionID); n != 1 {
		t.Fatal("сессия не отозвана удалением")
	}
}
