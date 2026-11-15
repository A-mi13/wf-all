package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
)

// «Где вы вошли»: мобильная и веб-сессия — поля устройства, браузер из UA у веба, current; IP нигде не отдаётся;
// отозванные и истёкшие не видны; новые сверху по last_seen_at.
func TestListSessionsShape(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, mob := loggedIn(t, e, "where", iphone)
	e.clock.Advance(time.Minute)
	browser := web("label-1")
	browser.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15"
	out, err := e.a.Login(context.Background(), "where@example.com", "secret", browser)
	if err != nil {
		t.Fatal(err)
	}
	wb := out.Issued.SessionID
	// отозванная и истёкшая — не видны
	gone, err := e.a.Login(context.Background(), "where@example.com", "secret", web("label-2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.a.Logout(context.Background(), gone.Issued.SessionID); err != nil {
		t.Fatal(err)
	}
	old, err := e.a.Login(context.Background(), "where@example.com", "secret", web("label-3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(context.Background(), `UPDATE sessions SET idle_expires_at = $2 WHERE id = $1`,
		old.Issued.SessionID, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	list, err := e.a.ListSessions(context.Background(), uid, wb)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != wb || list[1].ID != mob.SessionID {
		t.Fatalf("%+v", list)
	}
	w, m := list[0], list[1]
	if !w.Current || w.ClientKind != domain.ClientWeb || w.Browser != "Safari" || w.OS != "macOS" ||
		w.Platform != "" || w.Model != "" || w.AppVersion != "" || w.AuthMethod != string(domain.MethodPassword) {
		t.Fatalf("веб: %+v", w)
	}
	if m.Current || m.ClientKind != domain.ClientMobile || m.Platform != "ios" || m.Model != "iPhone15,2" ||
		m.OSVersion != "18.1" || m.AppVersion != "1.4.0" || m.Browser != "" {
		t.Fatalf("телефон: %+v", m)
	}
	if !m.CreatedAt.Equal(testNow) || !w.LastSeenAt.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("время: %+v", list)
	}
	// IP (203.0.113.7) — ни в одном поле
	for _, s := range list {
		v := reflect.ValueOf(s)
		for i := range v.NumField() {
			if f := v.Field(i); strings.Contains(f.String(), "203.0.113") || strings.Contains(
				strings.ToLower(v.Type().Field(i).Name), "ip") {
				t.Fatalf("IP в поле %s: %v", v.Type().Field(i).Name, f)
			}
		}
	}
	// чужой аккаунт своих сессий не видит
	other := seedUser(t, e.owner, "where_other", "ru")
	if l, err := e.a.ListSessions(context.Background(), other, uuid.Nil); err != nil || len(l) != 0 {
		t.Fatalf("чужой: %v %v", l, err)
	}
}

// revokeMySession: чужая и несуществующая — 404 session_not_found (одинаково); своя — nil, повтор (из другой живой
// сессии) — 404; текущую тоже можно; аудит sessions_revoked.
func TestRevokeSessionIDOR(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, mine := loggedIn(t, e, "idor_me", iphone)
	_, theirs := loggedIn(t, e, "idor_they", iphone)
	spareOut, err := e.a.Login(context.Background(), "idor_me@example.com", "secret", web("spare"))
	if err != nil || spareOut.Issued == nil {
		t.Fatalf("вход: %+v %v", spareOut, err)
	}
	spare := spareOut.Issued
	ctx := context.Background()
	for _, sid := range []uuid.UUID{theirs.SessionID, uuid.New()} {
		p := problemOf(t, e.a.RevokeSession(ctx, uid, mine.SessionID, sid))
		if p.Status != 404 || p.Code != identity.CodeSessionNotFound {
			t.Fatalf("%v: %+v", sid, p)
		}
	}
	if r := revokedReason(t, e, theirs.SessionID); r != "" {
		t.Fatalf("чужая отозвана: %q", r)
	}
	if err := e.a.RevokeSession(ctx, uid, mine.SessionID, mine.SessionID); err != nil {
		t.Fatal(err)
	}
	if r := revokedReason(t, e, mine.SessionID); r != string(domain.RevokedByUser) {
		t.Fatalf("причина %q", r)
	}
	if got := problemCode(e.a.RevokeSession(ctx, uid, spare.SessionID, mine.SessionID)); got != identity.CodeSessionNotFound {
		t.Fatalf("повтор: %q", got)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_user_id = $2`,
		ActionSessionsRevoked, uid); n != 1 {
		t.Fatalf("аудит %d", n)
	}
}

// revokeOtherSessions: 3 сессии → 2 отозвано (revoked_others), текущая жива, событие sessions_revoked; повтор — 0.
func TestRevokeOthers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, cur := loggedIn(t, e, "others", iphone)
	for _, l := range []string{"a", "b"} {
		if _, err := e.a.Login(context.Background(), "others@example.com", "secret", web(l)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := e.a.RevokeOthers(context.Background(), uid, cur.SessionID)
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if r := revokedReason(t, e, cur.SessionID); r != "" {
		t.Fatalf("текущая отозвана: %q", r)
	}
	if c := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_reason = 'revoked_others'`, uid); c != 2 {
		t.Fatalf("revoked_others: %d", c)
	}
	if c := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1`, identity.EventSessionsRevoked); c != 1 {
		t.Fatalf("событий %d", c)
	}
	if n, err := e.a.RevokeOthers(context.Background(), uid, cur.SessionID); err != nil || n != 0 {
		t.Fatalf("повтор: %d %v", n, err)
	}
}

type allProviders struct{}

func (allProviders) Apple() bool    { return true }
func (allProviders) Google() bool   { return true }
func (allProviders) Passkeys() bool { return true }

// getAuthMethods: флаги выключены — всё false (password — true); флаги включены, провайдеров нет — false; всё
// включено — true; auth_google включён при выключенном Apple — google.signup = false.
func TestAuthMethodsFlagsAndProviders(t *testing.T) {
	t.Parallel()
	set := func(t *testing.T, e *env, on bool, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, err := e.owner.Exec(context.Background(), `UPDATE feature_flags SET enabled_globally = $2 WHERE key = $1`, k, on); err != nil {
				t.Fatal(err)
			}
		}
	}
	all := []string{"auth_apple", "auth_apple_login", "auth_google", "auth_google_login", "auth_passkeys", "auth_passkeys_login"}
	on := AuthMethodsView{Password: true, AppleSignup: true, AppleLogin: true, GoogleSignup: true, GoogleLogin: true,
		PasskeysRegister: true, PasskeysLogin: true}

	none := newEnv(t)
	set(t, none, true, all...)
	if v, err := none.a.AuthMethods(context.Background()); err != nil || v != (AuthMethodsView{Password: true}) {
		t.Fatalf("без провайдеров: %+v %v", v, err)
	}

	e := newEnv(t, func(d *identity.Deps) { d.Providers = allProviders{} })
	set(t, e, false, all...)
	if v, err := e.a.AuthMethods(context.Background()); err != nil || v != (AuthMethodsView{Password: true}) {
		t.Fatalf("флаги выключены: %+v %v", v, err)
	}
	set(t, e, true, all...)
	if v, err := e.a.AuthMethods(context.Background()); err != nil || v != on {
		t.Fatalf("всё включено: %+v %v", v, err)
	}
	set(t, e, false, "auth_apple")
	want := on
	want.AppleSignup, want.GoogleSignup = false, false
	if v, err := e.a.AuthMethods(context.Background()); err != nil || v != want {
		t.Fatalf("Apple выключен: %+v %v", v, err)
	}
}

// m2 ревью W4-Б: текущая сессия перепроверяется под замком аккаунта — отозванная B (кэш загрузчика ≤ 5 с ещё
// пропускает её) не выкидывает владельца: revoke-others и DELETE /me/sessions/{A} — 401, сессия A жива.
func TestRevokeFromRevokedSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, a := loggedIn(t, e, "stale_cur", iphone)
	outB, err := e.a.Login(context.Background(), "stale_cur@example.com", "secret", web("b"))
	if err != nil || outB.Issued == nil {
		t.Fatalf("вход B: %+v %v", outB, err)
	}
	b := outB.Issued.SessionID
	if err := e.a.RevokeSession(context.Background(), uid, a.SessionID, b); err != nil {
		t.Fatal(err)
	}
	n, err := e.a.RevokeOthers(context.Background(), uid, b)
	if p := problemOf(t, err); p.Status != 401 || p.Code != httpx.CodeUnauthenticated || n != 0 {
		t.Fatalf("revoke-others: %d %+v", n, p)
	}
	if p := problemOf(t, e.a.RevokeSession(context.Background(), uid, b, a.SessionID)); p.Status != 401 ||
		p.Code != httpx.CodeUnauthenticated {
		t.Fatalf("revoke A из B: %+v", p)
	}
	// чужая «текущая» — как несуществующая
	_, other := loggedIn(t, e, "stale_other", iphone)
	if _, err := e.a.RevokeOthers(context.Background(), uid, other.SessionID); problemCode(err) != httpx.CodeUnauthenticated {
		t.Fatalf("чужая текущая: %v", err)
	}
	if r := revokedReason(t, e, a.SessionID); r != "" {
		t.Fatalf("A отозвана: %q", r)
	}
	if r := revokedReason(t, e, other.SessionID); r != "" {
		t.Fatalf("чужая отозвана: %q", r)
	}
}

// m2: истёкшая по idle текущая (ровно в момент idle_expires_at — уже мёртвая, как в загрузчике) — 401, остальные
// сессии аккаунта живы.
func TestRevokeFromExpiredSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, mob := loggedIn(t, e, "stale_exp", iphone)
	outW, err := e.a.Login(context.Background(), "stale_exp@example.com", "secret", web("w"))
	if err != nil || outW.Issued == nil {
		t.Fatalf("вход: %+v %v", outW, err)
	}
	var idle time.Time
	if err := e.owner.QueryRow(context.Background(), `SELECT idle_expires_at FROM sessions WHERE id = $1`,
		outW.Issued.SessionID).Scan(&idle); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(idle)
	if _, err := e.a.RevokeOthers(context.Background(), uid, outW.Issued.SessionID); problemCode(err) != httpx.CodeUnauthenticated {
		t.Fatalf("revoke-others из истёкшей: %v", err)
	}
	if r := revokedReason(t, e, mob.SessionID); r != "" {
		t.Fatalf("мобильная отозвана: %q", r)
	}
}

// Непредусмотренное 1 ревью W5-А: «выйти везде» гасит живые токены шагов аккаунта (mfa_token) — и когда других
// сессий нет (n = 0).
func TestRevokeOthersKillsStepTokens(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, _, tok := mfaLogin(t, e, "others_tokens", "abcde-fghij")
	cur := issue(t, e, uid, web("cur")).SessionID
	if n, err := e.a.RevokeOthers(context.Background(), uid, cur); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := complete(e, tok, SecondFactor{Recovery: "abcde-fghij"}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("mfa_token после «выйти везде»: %v", err)
	}
}
