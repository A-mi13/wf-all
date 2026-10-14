package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/password"
	"wf/backend/internal/staff"
)

// holdAccount — отдельная транзакция держит замок строки учётки (FOR UPDATE) до release: сценарий,
// который полез бы за замком, ждал бы её.
func (e *env) holdAccount(staffID uuid.UUID) (release func()) {
	e.t.Helper()
	tx, err := e.owner.Begin(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, staffID); err != nil {
		e.t.Fatal(err)
	}
	release = func() { _ = tx.Rollback(context.Background()) }
	e.t.Cleanup(release)
	return release
}

// I-1 ревью: повторной отправке и принятию нечего делать (учётка active или disabled, приглашение в
// паузе, истекло, кода нет) — ответ без замков и записей: учётка под чужим замком не задерживает
// ответ. Иначе время ответа выдавало бы существование почты сильнее, чем у входа. Неверный код к
// годному приглашению (14.10.2026) — тоже без замков: только запись аудита неудачи.
func TestInviteIneligibleTakesNoLocks(t *testing.T) {
	e := newEnv(t)
	active := e.addMember("active@wf.example", memberOpts{})
	off := e.addMember("off@wf.example", memberOpts{Status: "disabled"})
	paused, _ := e.invitee("paused@wf.example", memberOpts{Role: "support"}, purposeInvite)
	noCode, _ := e.invitee("nocode@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.exec(`UPDATE staff_invites SET code_enc = NULL, code_key_id = NULL WHERE staff_id = $1`, noCode.ID)
	expired, _ := e.invitee("expired@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.exec(`UPDATE staff_invites SET expires_at = $2 WHERE staff_id = $1`, expired.ID, t0)
	valid, validCode := e.invitee("valid@wf.example", memberOpts{Role: "support"}, purposeInvite)
	for _, m := range []member{active, off, paused, noCode, expired, valid} {
		e.holdAccount(m.ID)
	}
	// неверный код к годному приглашению: ответ без замка учётки и строки, неудача — в аудите
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err := e.app.AcceptInvite(ctx, valid.Email, otherCode(validCode), newPassword, "", metaWeb)
	cancel()
	if !errors.Is(err, staff.ErrInviteInvalid) || time.Since(start) > 3*time.Second {
		t.Fatalf("неверный код к годному: %v за %v", err, time.Since(start))
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != 1 {
		t.Fatalf("аудит неудачи %d", n)
	}

	for _, m := range []member{active, off, paused} {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := e.app.ResendInviteSelf(ctx, m.Email, metaWeb)
		cancel()
		if err != nil {
			t.Fatalf("повторная отправка %s: %v", m.Email, err)
		}
	}
	for _, m := range []member{active, off, noCode, expired} {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := e.app.AcceptInvite(ctx, m.Email, "k7m2p-x4q6d", newPassword, "", metaWeb)
		cancel()
		if !errors.Is(err, staff.ErrInviteInvalid) || time.Since(start) > 3*time.Second {
			t.Fatalf("принятие %s: %v за %v", m.Email, err, time.Since(start))
		}
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != 1 {
		t.Fatalf("записи у негодных: аудит неудач %d", n)
	}
}

// Шесть параллельных неверных кодов и верный в ту же секунду (общий старт): верный принят, код жив,
// каждая неверная — своя запись аудита (параллельные неудачи не теряются и ничего не стирают).
func TestAcceptInviteConcurrentWrong(t *testing.T) {
	e := newEnv(t)
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	before := e.inviteSecret(m.ID)
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		errs  [7]error
	)
	for i := range errs {
		in := otherCode(code)
		if i == len(errs)-1 {
			in = code
		}
		wg.Go(func() {
			<-start
			_, errs[i] = e.app.AcceptInvite(context.Background(), m.Email, in, newPassword, "", metaWeb)
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs[:6] {
		if !errors.Is(err, staff.ErrInviteInvalid) {
			t.Fatalf("неверная попытка %d: %v", i, err)
		}
	}
	if errs[6] != nil {
		t.Fatalf("верный код: %v", errs[6])
	}
	if got := e.inviteSecret(m.ID); got != before {
		t.Fatalf("код изменён: %+v → %+v", before, got)
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != 6 {
		t.Fatalf("аудит неудач %d", n)
	}
	e.clock.Advance(time.Minute)
	e.accept(m, code) // код жив и после гонки
}

// Код прочитан без замков и совпал, но пока принятие ждало замок учётки, админ выпустил новое
// приглашение: под замком сверка идёт с кодом новой строки — старый код отвергается (с записью
// неудачи), сессии нет.
func TestAcceptInviteReissuedWhileWaiting(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, oldCode := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	tx, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, m.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.app.AcceptInvite(ctx, m.Email, oldCode, newPassword, "", metaWeb)
		done <- err
	}()
	e.waitLockWaiters(1)
	newCode, err := e.app.IssueInvite(ctx, tx, m.ID, purposeInvite)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("старый код после перевыпуска: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE staff_id = $1`, m.ID); n != 0 {
		t.Fatalf("сессий %d", n)
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != 1 {
		t.Fatalf("аудит неудачи %d", n)
	}
	e.accept(m, newCode)
}

// waitLockWaiters — ждёт, пока n запросов базы теста встанут в ожидание замка (барьер вместо sleep).
func (e *env) waitLockWaiters(n int) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("ожидающих замок %d, ждали %d", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// cancelingHasher — хэшер, после которого клиент «оборвал соединение» (отмена ctx запроса).
type cancelingHasher struct {
	*password.Hasher
	cancel context.CancelFunc
}

func (h cancelingHasher) Hash(ctx context.Context, pw string) (string, error) {
	defer h.cancel()
	return h.Hasher.Hash(ctx, pw)
}

// M-3: обрыв соединения между хэшем пароля и транзакцией не теряет запись неверной попытки (detached).
func TestAcceptInviteCanceledAfterHash(t *testing.T) {
	e := newEnv(t)
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	wrong := otherCode(code)
	h, err := sharedHasher()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.app.SetHasher(cancelingHasher{Hasher: h, cancel: cancel})
	if _, err := e.app.AcceptInvite(ctx, m.Email, wrong, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("неверный код после обрыва: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("ctx не отменён — проверка вхолостую")
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != 1 {
		t.Fatalf("запись неудачи потеряна: %d", n)
	}
}

// M-3: два одновременных bootstrap одной почты — один успех, второй staff.email_taken.
func TestOperatorBootstrapConcurrent(t *testing.T) {
	e := newEnv(t)
	var (
		wg   sync.WaitGroup
		errs [2]error
	)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = e.app.OperatorBootstrap(context.Background(), "first@wf.example", "Первый")
		})
	}
	wg.Wait()
	ok, taken := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, staff.ErrEmailTaken):
			taken++
		default:
			t.Fatalf("bootstrap: %v", err)
		}
	}
	if ok != 1 || taken != 1 || e.count(`SELECT count(*) FROM staff_accounts`) != 1 {
		t.Fatalf("успехов %d, занято %d", ok, taken)
	}
}
