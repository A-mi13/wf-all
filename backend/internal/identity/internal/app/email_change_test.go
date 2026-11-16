package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/httpx"
)

// §3.9, I7: занятая и свободная новая почта — одинаковый 202; письма — email_change_taken и email_change_code;
// приманка несёт user_id вызывающего; подтверждение неверным кодом у приманки и настоящего — одинаково на 1-й, 4-й и
// 5-й попытке (attempts_left 4, 1, затем 410).
func TestEmailChangeTakenIndistinguishable(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "mail_change", "ru")
	seedUser(t, e.owner, "mail_taken", "ru")
	sid := issue(t, e, uid, web("x")).SessionID
	now := e.clock.Now()
	free, err := e.a.StartEmailChange(ctx, sid, " Mail_New@Example.com ", web("req"))
	if err != nil {
		t.Fatal(err)
	}
	taken, err := e.a.StartEmailChange(ctx, sid, "mail_taken@example.com", web("req"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []CodeSent{free, taken} {
		if s.ExpiresAt.Sub(now) != 15*60*1e9 || s.ResendAvailable.Sub(now) != 60*1e9 {
			t.Fatalf("%+v", s)
		}
	}
	if count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND NOT decoy AND user_id = $2 AND email = 'mail_new@example.com'`,
		free.CodeID, uid) != 1 ||
		count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND decoy AND user_id = $2`, taken.CodeID, uid) != 1 {
		t.Fatal("строки кодов")
	}
	if mailJobs(t, e, MailEmailChangeCode) != 1 || mailJobs(t, e, MailEmailChangeTaken) != 1 {
		t.Fatal("письма")
	}
	wrong := wrongCode(plainCode(t, e, free.CodeID))
	for i := 1; i <= 5; i++ {
		var got [2]string
		for j, id := range []uuid.UUID{free.CodeID, taken.CodeID} {
			_, err := e.a.ConfirmEmailChange(ctx, uid, id, wrong)
			p := problemOfErr(err)
			got[j] = p.Code
			if p.AttemptsLeft != nil {
				got[j] += strconv.Itoa(*p.AttemptsLeft)
			}
		}
		if got[0] != got[1] {
			t.Fatalf("попытка %d: настоящий %q, приманка %q", i, got[0], got[1])
		}
		if (i == 1 && got[0] != identity.CodeCodeInvalid+"4") || (i == 4 && got[0] != identity.CodeCodeInvalid+"1") ||
			(i == 5 && got[0] != identity.CodeCodeExpired) {
			t.Fatalf("попытка %d: %q", i, got[0])
		}
	}
	if _, err := e.a.StartEmailChange(ctx, sid, "mail_change@example.com", web("req")); problemCode(err) != identity.CodeEmailSame {
		t.Fatalf("та же почта: %v", err)
	}
	if _, err := e.a.StartEmailChange(ctx, sid, "x@mailinator.com", web("req")); problemCode(err) != identity.CodeEmailDisposable {
		t.Fatalf("одноразовая: %v", err)
	}
	if _, err := e.a.StartEmailChange(ctx, sid, "не почта", web("req")); problemCode(err) != httpx.CodeValidationFailed {
		t.Fatalf("неверная: %v", err)
	}
	stale := staleSession(t, e, uid)
	if _, err := e.a.StartEmailChange(ctx, stale, "other_new@example.com", web("req")); problemCode(err) != identity.CodeReauthRequired {
		t.Fatalf("несвежая: %v", err)
	}
}

// §3.9 п. 2: успех — почта сменена (email_verified_at, version + 1), старая — в meta_enc журнала email_changed, письмо
// email_changed ссылается на эту запись (адрес собирается из старой почты), событие без адреса; сессии живы; Me —
// новая почта.
func TestConfirmEmailChangeHappy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "mail_happy", "ru")
	sid := issue(t, e, uid, web("x")).SessionID
	sent, err := e.a.StartEmailChange(ctx, sid, "mail_happy_new@example.com", web("req"))
	if err != nil {
		t.Fatal(err)
	}
	me, err := e.a.ConfirmEmailChange(ctx, uid, sent.CodeID, plainCode(t, e, sent.CodeID))
	if err != nil || me.Email != "mail_happy_new@example.com" {
		t.Fatalf("%+v %v", me, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND email = 'mail_happy_new@example.com' AND version = 2`, uid); n != 1 {
		t.Fatal("почта не сменена")
	}
	var refID uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT (args->>'ref')::uuid FROM river_job WHERE args->>'mail' = $1`, MailEmailChanged).
		Scan(&refID); err != nil {
		t.Fatal(err)
	}
	var enc []byte
	var keyID string
	if err := e.owner.QueryRow(ctx, `SELECT meta_enc, key_id FROM security_events WHERE id = $1 AND user_id = $2
		AND kind = 'email_changed'`, refID, uid).Scan(&enc, &keyID); err != nil {
		t.Fatal(err)
	}
	addr, err := e.a.secrets.Open(enc, keyID, refID[:])
	if err != nil || string(addr) != "mail_happy@example.com" {
		t.Fatalf("адрес письма email_changed: %q %v", addr, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = 'identity.user_email_changed' AND aggregate_id = $1
		AND payload::text NOT LIKE '%@%'`, uid); n != 1 {
		t.Fatal("событие")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = 'identity.email_changed' AND actor_user_id = $1`, uid); n != 1 {
		t.Fatal("аудит")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid); n != 1 {
		t.Fatal("сессия отозвана")
	}
}

// Код другого пользователя — 410, попытка не засчитана.
func TestConfirmEmailChangeForeignCode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	owner := seedUser(t, e.owner, "mail_owner", "ru")
	thief := seedUser(t, e.owner, "mail_thief", "ru")
	sent, err := e.a.StartEmailChange(ctx, issue(t, e, owner, web("x")).SessionID, "mail_owner_new@example.com", web("req"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.ConfirmEmailChange(ctx, thief, sent.CodeID, plainCode(t, e, sent.CodeID)); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("чужой код: %v", err)
	}
	if codeAttempts(t, e, sent.CodeID) != 0 || !codeAlive(t, e, sent.CodeID) {
		t.Fatal("чужой код тронут")
	}
}

// Почту заняли между стартом и подтверждением — 409 email_taken, почта прежняя.
func TestConfirmEmailChangeRace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	uid := seedUser(t, e.owner, "mail_race", "ru")
	sent, err := e.a.StartEmailChange(ctx, issue(t, e, uid, web("x")).SessionID, "mail_race_new@example.com", web("req"))
	if err != nil {
		t.Fatal(err)
	}
	seedUser(t, e.owner, "mail_race_new", "ru")
	if _, err := e.a.ConfirmEmailChange(ctx, uid, sent.CodeID, plainCode(t, e, sent.CodeID)); problemCode(err) != identity.CodeEmailTaken {
		t.Fatalf("занята: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND email = 'mail_race@example.com'`, uid); n != 1 {
		t.Fatal("почта сменена")
	}
}
