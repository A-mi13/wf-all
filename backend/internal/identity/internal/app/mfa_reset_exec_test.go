package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
)

// Исполнение сброса 2FA по сроку (спека §3.15 п. 5; план I-2, Task 18): воркер под ролью worker — users SKIP LOCKED
// (R30), перепроверка 2FA и «живого» под замками; стираются TOTP и резервные коды, сессии отзываются, ключи доступа
// остаются.

// withRecoveryAndPasskey — резервный код и ключ доступа игрока (фикстура).
func (r *resetEnv) withRecoveryAndPasskey(t *testing.T, uid uuid.UUID) {
	t.Helper()
	acctExec(t, r.env, `INSERT INTO user_recovery_codes (user_id, code_mac, key_id, created_at)
		VALUES ($1, decode(repeat('ab', 32), 'hex'), 'AAAAAAAAAAA', now())`, uid)
	acctExec(t, r.env, `INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count, backup_eligible,
		backup_state, attestation_format, name, created_at)
		VALUES (gen_random_uuid(), $1, decode(repeat('01', 16), 'hex'), '\x02', gen_random_uuid(), 0, false, false, 'none', 'Ключ', now())`, uid)
}

func TestExecuteDueMfaResets(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	ctx := context.Background()
	uid := seedUser(t, r.owner, "reset_due", "ru")
	r.withMfa(t, uid, true)
	r.withRecoveryAndPasskey(t, uid)
	issue(t, r.env, uid, web("a"))
	issue(t, r.env, uid, web("b"))
	rid := r.liveReset(t, uid)

	// до срока — ничего
	r.clock.Advance(domain.MfaResetDelay - time.Second)
	if n, _, err := w.ExecuteDueMfaResets(ctx, 10, nil); err != nil || n != 0 {
		t.Fatalf("до срока: %d %v", n, err)
	}
	if count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, uid) != 1 {
		t.Fatal("2FA снята до срока")
	}
	// ровно в срок — исполнение
	r.clock.Advance(time.Second)
	if n, _, err := w.ExecuteDueMfaResets(ctx, 10, nil); err != nil || n != 1 {
		t.Fatalf("в срок: %d %v", n, err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM user_mfa WHERE user_id = $1`:                                0,
		`SELECT count(*) FROM user_recovery_codes WHERE user_id = $1`:                     0,
		`SELECT count(*) FROM passkeys WHERE user_id = $1`:                                1,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`:         0,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_reason = 'revoked'`: 2,
		`SELECT count(*) FROM mfa_resets WHERE user_id = $1 AND executed_at IS NOT NULL`:  1,
		`SELECT count(*) FROM audit_log WHERE action = 'identity.mfa_reset_executed' AND object_id = $1
			AND actor_role = 'system'`: 1,
	} {
		if n := count(t, r.owner, q, uid); n != want {
			t.Errorf("%s: %d, ждали %d", q, n, want)
		}
	}
	if n := count(t, r.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2
		AND payload->>'enabled' = 'false'`, identity.EventMfaChanged, uid); n != 1 {
		t.Errorf("событие mfa_changed: %d", n)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2`,
		identity.EventSessionsRevoked, uid); n != 1 {
		t.Errorf("событие sessions_revoked: %d", n)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM river_job WHERE queue = 'mail' AND args->>'mail' = $1 AND args->>'ref' = $2`,
		MailMfaResetDone, rid.String()); n != 1 {
		t.Errorf("письмо mfa_reset_done: %d", n)
	}
	// повтор — ничего
	if n, _, err := w.ExecuteDueMfaResets(ctx, 10, nil); err != nil || n != 0 {
		t.Fatalf("повтор: %d %v", n, err)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1`, MailMfaResetDone); n != 1 {
		t.Fatal("второе письмо при повторе")
	}
}

// TestExecuteKeepsPasskeys — ключи доступа остаются после исполнения (способ входа; удаляет владелец сам).
func TestExecuteKeepsPasskeys(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	uid := seedUser(t, r.owner, "reset_passkey", "ru")
	r.withMfa(t, uid, true)
	r.withRecoveryAndPasskey(t, uid)
	r.liveReset(t, uid)
	r.clock.Advance(domain.MfaResetDelay)
	if n, _, err := w.ExecuteDueMfaResets(context.Background(), 10, nil); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if count(t, r.owner, `SELECT count(*) FROM passkeys WHERE user_id = $1`, uid) != 1 {
		t.Fatal("ключ доступа удалён")
	}
}

// TestExecuteCancelsStale — к сроку 2FA уже нет или аккаунт в удалении: сброс отменяет система (событие версии 2, без
// письма), сессии не трогаются.
func TestExecuteCancelsStale(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	noMfa := seedUser(t, r.owner, "reset_gone_mfa", "ru")
	ridA := r.liveReset(t, noMfa)
	deleting := seedUser(t, r.owner, "reset_deleting", "ru")
	r.withMfa(t, deleting, true)
	ridB := r.liveReset(t, deleting)
	acctExec(t, r.env, `UPDATE users SET status = 'pending_deletion', status_before_deletion = status,
		deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`, deleting)
	sid := issue(t, r.env, noMfa, web("a")).SessionID
	r.clock.Advance(domain.MfaResetDelay)
	if n, _, err := w.ExecuteDueMfaResets(context.Background(), 10, nil); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	for _, rid := range []uuid.UUID{ridA, ridB} {
		if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_by = 'system' AND executed_at IS NULL`, rid); n != 1 {
			t.Errorf("сброс %s не отменён системой", rid)
		}
		if n := count(t, r.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2 AND aggregate_version = 2`,
			identity.EventMfaResetCancelled, rid); n != 1 {
			t.Errorf("событие отмены %s: %d", rid, n)
		}
	}
	// аудит отмены от system (спека §8.3; m4 ревью 3)
	for _, uid := range []uuid.UUID{noMfa, deleting} {
		if n := count(t, r.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2 AND actor_role = 'system'
			AND after->>'cancelled_by' = 'system'`, ActionMfaResetCancelled, uid); n != 1 {
			t.Errorf("аудит системной отмены %s: %d", uid, n)
		}
	}
	if n := count(t, r.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' IN ($1, $2)`, MailMfaResetDone, MailMfaResetCancelled); n != 0 {
		t.Fatalf("письма при отмене системой: %d", n)
	}
	if count(t, r.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid) != 1 ||
		count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, deleting) != 1 {
		t.Fatal("отменённый сброс что-то стёр")
	}
}

// TestExecuteSkipsLockedUser — аккаунт под замком сценария игрока: воркер не ждёт (SKIP LOCKED), строку возьмёт
// следующий запуск.
func TestExecuteSkipsLockedUser(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	uid := seedUser(t, r.owner, "reset_locked", "ru")
	r.withMfa(t, uid, true)
	r.liveReset(t, uid)
	r.clock.Advance(domain.MfaResetDelay)
	ctx := context.Background()
	hold, err := r.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE`, uid); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	n, _, err := w.ExecuteDueMfaResets(wctx, 10, nil)
	if err != nil || n != 0 {
		t.Fatalf("под замком: %d %v", n, err)
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _, err := w.ExecuteDueMfaResets(ctx, 10, nil); err != nil || n != 1 {
		t.Fatalf("после замка: %d %v", n, err)
	}
}

// TestExecuteVsOwnerCancelRace — исполнение и явная отмена владельцем разом (барьер: воркер прочитал кандидатов,
// владелец взял замок аккаунта): ровно один исход — либо отменён (2FA цела, сессии живы), либо исполнен (отмена — 404).
func TestExecuteVsOwnerCancelRace(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	uid := seedUser(t, r.owner, "reset_race_exec", "ru")
	r.withMfa(t, uid, true)
	s, err := finish(t, r.env, uid, web("a"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	sid := s.Issued.SessionID
	r.clock.Advance(time.Minute)
	rid := r.liveReset(t, uid)
	r.clock.Advance(domain.MfaResetDelay)
	barrier := pairBarrier(3 * time.Second)
	w.hooks = &hooks{afterRowFound: func(kind string) {
		if kind == rowMfaResetDue {
			barrier()
		}
	}}
	r.a.hooks = &hooks{afterFirstLock: func(op string) {
		if op == opCancelMyMfaReset {
			barrier()
		}
	}}
	var (
		wg        sync.WaitGroup
		executed  int
		execErr   error
		cancelErr error
	)
	start := make(chan struct{})
	wg.Go(func() { <-start; executed, _, execErr = w.ExecuteDueMfaResets(context.Background(), 10, nil) })
	wg.Go(func() { <-start; cancelErr = r.a.CancelMyMfaReset(context.Background(), uid, sid) })
	close(start)
	wg.Wait()
	if execErr != nil {
		t.Fatalf("исполнение: %v", execErr)
	}
	cancelled := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_by = 'owner'`, rid)
	done := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND executed_at IS NOT NULL`, rid)
	mfa := count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, uid)
	live := count(t, r.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid)
	switch {
	case cancelled == 1 && done == 0 && cancelErr == nil && executed == 0 && mfa == 1 && live == 1:
	case done == 1 && cancelled == 0 && executed == 1 && mfa == 0 && live == 0:
		if status, _ := problemStatus(cancelErr); status != http.StatusNotFound && !errors.Is(cancelErr, errSessionGone) {
			t.Fatalf("отмена после исполнения: %v", cancelErr)
		}
	default:
		t.Fatalf("не один исход: отменён %d, исполнен %d (n=%d), 2FA %d, сессия %d, отмена %v", cancelled, done, executed, mfa, live, cancelErr)
	}
}

// TestExecuteRechecksAfterCancel — владелец отменил сброс между чтением кандидатов и замками воркера (отмена
// закоммичена в хуке после выборки): воркер перепроверяет «живой» под замком строки и ничего не стирает.
func TestExecuteRechecksAfterCancel(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	uid := seedUser(t, r.owner, "reset_recheck", "ru")
	r.withMfa(t, uid, true)
	s, err := finish(t, r.env, uid, web("a"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	sid := s.Issued.SessionID
	rid := r.liveReset(t, uid)
	r.clock.Advance(domain.MfaResetDelay)
	var cancelErr error
	w.hooks = &hooks{afterRowFound: func(kind string) {
		if kind == rowMfaResetDue {
			cancelErr = r.a.CancelMyMfaReset(context.Background(), uid, sid)
		}
	}}
	n, _, err := w.ExecuteDueMfaResets(context.Background(), 10, nil)
	if err != nil || cancelErr != nil || n != 0 {
		t.Fatalf("исполнение после отмены: %d %v, отмена %v", n, err, cancelErr)
	}
	if count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_by = 'owner' AND executed_at IS NULL`, rid) != 1 ||
		count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, uid) != 1 ||
		count(t, r.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid) != 1 {
		t.Fatal("воркер исполнил отменённый сброс")
	}
}

// TestExecutePoisonedRowDoesNotStopOthers — m2 ревью W5: строка, на которой исполнение падает (триггер на mfa_resets),
// первой в выборке: она откатывается одна и уходит в failed (воркер пропустит её до конца прогона), остальные
// исполняются, вызов не обрывается; с skip упавшая не выбирается.
func TestExecutePoisonedRowDoesNotStopOthers(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	w := roleApp(t, r.env, "worker", true)
	ctx := context.Background()
	bad := seedUser(t, r.owner, "reset_poison", "ru")
	r.withMfa(t, bad, true)
	ridBad := r.liveReset(t, bad) // UUIDv7 раньше — первой в ORDER BY execute_at, id
	good := seedUser(t, r.owner, "reset_healthy", "ru")
	r.withMfa(t, good, true)
	ridGood := r.liveReset(t, good)
	acctExec(t, r.env, `CREATE FUNCTION test_poison_reset() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'отравленная строка'; END $$`)
	acctExec(t, r.env, `CREATE TRIGGER test_poison_reset BEFORE UPDATE ON mfa_resets FOR EACH ROW
		WHEN (OLD.id = '`+ridBad.String()+`'::uuid) EXECUTE FUNCTION test_poison_reset()`)
	r.clock.Advance(domain.MfaResetDelay)
	n, failed, err := w.ExecuteDueMfaResets(ctx, 10, nil)
	if err != nil || n != 1 || len(failed) != 1 || failed[0] != ridBad {
		t.Fatalf("исполнено %d, упали %v, ошибка %v", n, failed, err)
	}
	if count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND executed_at IS NOT NULL`, ridGood) != 1 ||
		count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, good) != 0 {
		t.Fatal("здоровая строка не исполнена")
	}
	if count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND executed_at IS NULL AND cancelled_at IS NULL`, ridBad) != 1 ||
		count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, bad) != 1 {
		t.Fatal("упавшая строка не откатилась")
	}
	// в skip — не выбирается
	if n, failed, err := w.ExecuteDueMfaResets(ctx, 10, []uuid.UUID{ridBad}); err != nil || n != 0 || len(failed) != 0 {
		t.Fatalf("со skip: %d %v %v", n, failed, err)
	}
}
