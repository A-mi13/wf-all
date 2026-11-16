package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
)

// Сброс 2FA поддержкой — отмена владельцем (спека §3.15 п. 4, 6; план I-2, Task 18): вход вторым фактором отменяет
// живой сброс в транзакции входа (хук onSecondFactorLogin), явная отмена — cancelMyMfaReset.

// ownerCancelled — сброс rid отменён владельцем: событие (версия 2), письмо, аудит от игрока.
func ownerCancelled(t *testing.T, e *env, uid, rid uuid.UUID) {
	t.Helper()
	if n := count(t, e.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_by = 'owner' AND cancelled_at IS NOT NULL`, rid); n != 1 {
		t.Fatal("сброс не отменён владельцем")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2 AND aggregate_version = 2`,
		identity.EventMfaResetCancelled, rid); n != 1 {
		t.Fatalf("событие отмены: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1 AND args->>'ref' = $2`,
		MailMfaResetCancelled, rid.String()); n != 1 {
		t.Fatalf("письмо отмены: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_user_id = $2 AND object_id = $2`,
		ActionMfaResetCancelled, uid); n != 1 {
		t.Fatalf("аудит отмены: %d", n)
	}
}

func TestSecondFactorLoginCancelsReset(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	uid := seedUser(t, r.owner, "reset_login", "ru")
	r.withMfa(t, uid, true)
	rid := r.liveReset(t, uid)
	// вход без второго фактора (шаг пароля прошёл, mfaDone = false) сброс не трогает
	if _, err := finish(t, r.env, uid, web("a"), false, false); err != nil {
		t.Fatal(err)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_at IS NULL`, rid); n != 1 {
		t.Fatal("сброс отменён без второго фактора")
	}
	out, err := finish(t, r.env, uid, web("a"), true, false)
	if err != nil || out.Status != StatusAuthenticated {
		t.Fatalf("вход вторым фактором: %v %+v", err, out)
	}
	ownerCancelled(t, r.env, uid, rid)
}

func TestCancelMyMfaReset(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	ctx := context.Background()
	uid := seedUser(t, r.owner, "reset_mine", "ru")
	r.withMfa(t, uid, true)
	// сессия вошла вторым фактором до назначения сброса (mfa_at = testNow)
	s1, err := finish(t, r.env, uid, web("a"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	sid := s1.Issued.SessionID
	r.clock.Advance(time.Hour)
	rid := r.liveReset(t, uid)
	if err := r.a.CancelMyMfaReset(ctx, uid, sid); err != nil {
		t.Fatalf("живой: %v", err)
	}
	ownerCancelled(t, r.env, uid, rid)
	// повтор той же сессией — 204 (отменён владельцем не раньше её входа), без второго письма
	if err := r.a.CancelMyMfaReset(ctx, uid, sid); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1`, MailMfaResetCancelled); n != 1 {
		t.Fatalf("писем отмены %d", n)
	}
	// сессия, вошедшая после отмены, — 404 (эта сессия не отменяла)
	r.clock.Advance(time.Hour)
	s2, err := finish(t, r.env, uid, web("b"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if status, code := problemStatus(r.a.CancelMyMfaReset(ctx, uid, s2.Issued.SessionID)); status != http.StatusNotFound ||
		code != identity.CodeMfaResetNotFound {
		t.Fatalf("после отмены новой сессией: %d %s", status, code)
	}
	// сброса не было вовсе — 404
	other := seedUser(t, r.owner, "reset_none", "ru")
	so := issue(t, r.env, other, web("c")).SessionID
	if status, code := problemStatus(r.a.CancelMyMfaReset(ctx, other, so)); status != http.StatusNotFound ||
		code != identity.CodeMfaResetNotFound {
		t.Fatalf("без сброса: %d %s", status, code)
	}
	// отозванная сессия (кэш загрузчика ещё помнит её) — 401, сброс жив
	rid2 := r.liveReset(t, other)
	acctExec(t, r.env, `UPDATE sessions SET revoked_at = now(), revoked_reason = 'logout' WHERE id = $1`, so)
	if status, _ := problemStatus(r.a.CancelMyMfaReset(ctx, other, so)); status != http.StatusUnauthorized {
		t.Fatalf("отозванная сессия: %d", status)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_at IS NULL`, rid2); n != 1 {
		t.Fatal("отозванная сессия отменила сброс")
	}
}

// m2 ревью 3: свежий вход TOTP после отмены (mfa_at сессии позже cancelled_at) не превращает повтор явной отмены той
// же сессией в 404 — сброс отменён владельцем за жизнь сессии.
func TestCancelMyMfaResetAfterReauth(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	ctx := context.Background()
	uid := seedUser(t, r.owner, "reset_reauth", "ru")
	secret := seedMfa(t, r.env, uid)
	rid := r.liveReset(t, uid)
	s, err := finish(t, r.env, uid, web("a"), true, false) // вход вторым фактором отменяет сброс
	if err != nil {
		t.Fatal(err)
	}
	ownerCancelled(t, r.env, uid, rid)
	r.clock.Advance(time.Minute)
	if _, err := r.a.Reauthenticate(ctx, s.Issued.SessionID, ReauthInput{TOTPCode: totpNow(r.env, secret)}); err != nil {
		t.Fatal(err)
	}
	if err := r.a.CancelMyMfaReset(ctx, uid, s.Issued.SessionID); err != nil {
		t.Fatalf("повтор после свежего входа: %v", err)
	}
}

// П. 5 волны F (решение пользователя 16.11): явная отмена — только сессией, прошедшей второй фактор (mfa_at задан).
// Без него при живом сбросе — 403 reauth_required, сброс жив, письма нет; без него и без живого сброса — 404, даже если
// сброс отменил владелец другой сессией за жизнь этой; после второго фактора — 204.
func TestCancelMyMfaResetNeedsSecondFactor(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	ctx := context.Background()
	uid := seedUser(t, r.owner, "reset_no_mfa", "ru")
	r.withMfa(t, uid, true)
	sid := issue(t, r.env, uid, web("a")).SessionID // mfa_at = NULL
	r.clock.Advance(time.Minute)
	rid := r.liveReset(t, uid)
	if status, code := problemStatus(r.a.CancelMyMfaReset(ctx, uid, sid)); status != http.StatusForbidden ||
		code != identity.CodeReauthRequired {
		t.Fatalf("без второго фактора: %d %s", status, code)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_at IS NULL`, rid); n != 1 {
		t.Fatal("сброс отменён сессией без второго фактора")
	}
	if n := count(t, r.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1`, MailMfaResetCancelled); n != 0 {
		t.Fatalf("письмо отмены: %d", n)
	}
	// другая сессия входит вторым фактором и отменяет сброс — эта (без второго фактора) его не отменяла: 404
	if _, err := finish(t, r.env, uid, web("b"), true, false); err != nil {
		t.Fatal(err)
	}
	if status, code := problemStatus(r.a.CancelMyMfaReset(ctx, uid, sid)); status != http.StatusNotFound ||
		code != identity.CodeMfaResetNotFound {
		t.Fatalf("отменён другой сессией: %d %s", status, code)
	}
	// новый сброс, сессия прошла второй фактор — 204
	r.clock.Advance(time.Minute)
	rid2 := r.liveReset(t, uid)
	acctExec(t, r.env, `UPDATE sessions SET mfa_at = $2 WHERE id = $1`, sid, r.clock.Now())
	if err := r.a.CancelMyMfaReset(ctx, uid, sid); err != nil {
		t.Fatalf("после второго фактора: %v", err)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_by = 'owner'`, rid2); n != 1 {
		t.Fatal("сброс не отменён владельцем")
	}
}

// TestMfaStatusShowsReset — назначенный сброс виден в getMyMfa (reset_scheduled_at) и в шаге 2FA входа (reset_at).
func TestMfaStatusShowsReset(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	uid := seedUser(t, r.owner, "reset_status", "ru")
	r.withMfa(t, uid, true)
	r.liveReset(t, uid)
	want := testNow.Add(7 * 24 * time.Hour)
	st, err := r.a.MfaStatus(context.Background(), uid)
	if err != nil || st.ResetScheduledAt == nil || !st.ResetScheduledAt.Equal(want) {
		t.Fatalf("MfaStatus: %v %+v", err, st)
	}
	out, err := finish(t, r.env, uid, web("a"), false, false)
	if err != nil || out.Status != StatusMfaRequired || out.ResetAt == nil || !out.ResetAt.Equal(want) {
		t.Fatalf("шаг 2FA: %v %+v", err, out)
	}
}

// TestCancelMyMfaResetOnlyLatest — m1 ревью W5: повтор отмены той же сессией — 204 только про последний сброс
// аккаунта. R1 отменён этой сессией, затем назначен R2 и отменён сотрудником — отмена из той же сессии: 404, а не 204.
func TestCancelMyMfaResetOnlyLatest(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	ctx := context.Background()
	uid := seedUser(t, r.owner, "reset_latest", "ru")
	r.withMfa(t, uid, true)
	s, err := finish(t, r.env, uid, web("a"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	sid := s.Issued.SessionID
	r.clock.Advance(time.Hour)
	r.liveReset(t, uid)
	if err := r.a.CancelMyMfaReset(ctx, uid, sid); err != nil {
		t.Fatalf("R1: %v", err)
	}
	r.clock.Advance(time.Hour)
	now := r.clock.Now()
	acctExec(t, r.env, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		execute_at, created_at, cancelled_at, cancelled_by) VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(),
		'Ольга', 'потерян телефон', $2, $3, $4, 'staff:Ольга')`, uid, now.Add(7*24*time.Hour), now, now.Add(time.Minute))
	if status, code := problemStatus(r.a.CancelMyMfaReset(ctx, uid, sid)); status != http.StatusNotFound ||
		code != identity.CodeMfaResetNotFound {
		t.Fatalf("R2 отменён сотрудником: %d %s", status, code)
	}
}
