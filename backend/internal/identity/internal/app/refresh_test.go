package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
)

const installID = "6F9619FF-8B86-4D01-B42D-00CF4FC964FF"

// loggedIn — вход аккаунта nick с паролем «secret» клиентом c; сессия выдана.
func loggedIn(t *testing.T, e *env, nick string, c Client) (uuid.UUID, Issued) {
	t.Helper()
	uid := loginUser(t, e, nick)
	out, err := e.a.Login(context.Background(), nick+"@example.com", "secret", c)
	if err != nil || out.Issued == nil {
		t.Fatalf("вход: %+v %v", out, err)
	}
	return uid, *out.Issued
}

func revokedReason(t *testing.T, e *env, sid uuid.UUID) string {
	t.Helper()
	var r *string
	if err := e.owner.QueryRow(context.Background(), `SELECT revoked_reason FROM sessions WHERE id = $1`, sid).Scan(&r); err != nil {
		t.Fatal(err)
	}
	if r == nil {
		return ""
	}
	return *r
}

func refreshInvalid(t *testing.T, err error, what string) {
	t.Helper()
	p := problemOf(t, err)
	if p.Status != 401 || p.Code != identity.CodeRefreshInvalid {
		t.Fatalf("%s: %+v", what, p)
	}
}

// §3.7: ротация — новый refresh ≠ старый; старый в окне 15 с с того же устройства — тот же преемник; после окна —
// отзыв сессии (refresh_reuse), security_events, письмо session_revoked_suspicious, 401.
func TestRefreshRotates(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, is := loggedIn(t, e, "rotate", iphone)
	ctx := context.Background()
	old := is.Tokens.Refresh
	p1, err := e.a.Refresh(ctx, old, installID, iphone)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Refresh == old || p1.Refresh == "" || p1.SessionID != is.SessionID || p1.Access == "" {
		t.Fatalf("%+v", p1)
	}
	e.clock.Advance(domain.RefreshRaceWindow - time.Second)
	p2, err := e.a.Refresh(ctx, old, installID, iphone)
	if err != nil || p2.Refresh != p1.Refresh {
		t.Fatalf("в окне: %+v %v", p2, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM refresh_tokens WHERE session_id = $1`, is.SessionID); n != 2 {
		t.Fatalf("токенов %d — окно выдало новую пару", n)
	}
	e.clock.Advance(time.Second) // ровно конец окна — уже повтор
	_, err = e.a.Refresh(ctx, old, installID, iphone)
	refreshInvalid(t, err, "после окна")
	if r := revokedReason(t, e, is.SessionID); r != string(domain.RevokedRefreshReuse) {
		t.Fatalf("причина %q", r)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE kind = 'refresh_reuse' AND user_id = $1
		AND session_id = $2`, uid, is.SessionID); n != 1 {
		t.Fatalf("refresh_reuse: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1`,
		MailSessionRevokedSuspicious); n != 1 {
		t.Fatalf("писем %d", n)
	}
	// преемник отозванной сессии тоже не действует
	_, err = e.a.Refresh(ctx, p1.Refresh, installID, iphone)
	refreshInvalid(t, err, "преемник после отзыва")
}

// Повтор в окне с другого устройства (другой install_id, другой вид клиента, сессия без устройства) — отзыв и 401.
func TestRefreshRaceOtherDevice(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		login, again Client
		install      string
	}{
		"другой install_id": {iphone, iphone, "11111111-2222-3333-4444-555555555555"},
		"с веба":            {iphone, web("label"), ""},
		"веб, другая метка": {web("label-a"), web("label-b"), ""},
		"веб без метки":     {web(""), web(""), ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, is := loggedIn(t, e, "race_"+uuid.NewString()[:8], c.login)
			first := installID
			if c.login.Kind == domain.ClientWeb {
				first = ""
			}
			if _, err := e.a.Refresh(ctx, is.Tokens.Refresh, first, c.login); err != nil {
				t.Fatal(err)
			}
			_, err := e.a.Refresh(ctx, is.Tokens.Refresh, c.install, c.again)
			refreshInvalid(t, err, "другое устройство")
			if r := revokedReason(t, e, is.SessionID); r != string(domain.RevokedRefreshReuse) {
				t.Fatalf("причина %q", r)
			}
		})
	}
	// веб с той же меткой в окне — тот же преемник
	_, is := loggedIn(t, e, "race_web_same", web("label-x"))
	p1, err := e.a.Refresh(ctx, is.Tokens.Refresh, "", web("label-x"))
	if err != nil {
		t.Fatal(err)
	}
	if p2, err := e.a.Refresh(ctx, is.Tokens.Refresh, "", web("label-x")); err != nil || p2.Refresh != p1.Refresh {
		t.Fatalf("веб, та же метка: %+v %v", p2, err)
	}
}

// Два параллельных refresh одного токена с одного устройства — оба 200, одна пара (второй ждёт замок и получает
// преемника первого).
func TestRefreshConcurrent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, is := loggedIn(t, e, "concurrent_refresh", iphone)
	// барьер: оба нашли строку токена без замка, потом идут к замку
	wait := pairBarrier(5 * time.Second)
	e.a.hooks = &hooks{afterRowFound: func(string) { wait() }}
	var wg sync.WaitGroup
	var got [2]TokenPair
	var errs [2]error
	for i := range 2 {
		wg.Go(func() { got[i], errs[i] = e.a.Refresh(context.Background(), is.Tokens.Refresh, installID, iphone) })
	}
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil {
			t.Fatalf("%d: %v", i, errs[i])
		}
	}
	if got[0].Refresh != got[1].Refresh {
		t.Fatal("две разные пары")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM refresh_tokens WHERE session_id = $1`, is.SessionID); n != 2 {
		t.Fatalf("токенов %d", n)
	}
	if r := revokedReason(t, e, is.SessionID); r != "" {
		t.Fatalf("сессия отозвана: %q", r)
	}
}

// Ротация продлевает только срок бездействия и не дальше абсолютного; после абсолютного — 401. Истёкший idle — 401.
func TestRefreshIdleExtendsWithinAbsolute(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, is := loggedIn(t, e, "idle_user", iphone)
	ctx := context.Background()
	idle, absolute := domain.SessionTerms(domain.ClientMobile)
	start := e.clock.Now()
	raw := is.Tokens.Refresh
	for e.clock.Now().Sub(start) < absolute-idle {
		e.clock.Advance(80 * 24 * time.Hour)
		p, err := e.a.Refresh(ctx, raw, installID, iphone)
		if err != nil {
			t.Fatalf("на %v: %v", e.clock.Now().Sub(start), err)
		}
		raw = p.Refresh
		want := e.clock.Now().Add(idle)
		if lim := start.Add(absolute); want.After(lim) {
			want = lim
		}
		if !p.RefreshIdleExpires.Equal(want) {
			t.Fatalf("idle %v, ждали %v", p.RefreshIdleExpires, want)
		}
	}
	var abs time.Time
	if err := e.owner.QueryRow(ctx, `SELECT absolute_expires_at FROM sessions WHERE id = $1`, is.SessionID).Scan(&abs); err != nil {
		t.Fatal(err)
	}
	if !abs.Equal(start.Add(absolute)) {
		t.Fatalf("абсолютный срок сдвинулся: %v", abs)
	}
	e.clock.Set(start.Add(absolute))
	_, err := e.a.Refresh(ctx, raw, installID, iphone)
	refreshInvalid(t, err, "ровно на абсолютном")

	_, is2 := loggedIn(t, e, "idle_user2", iphone)
	e.clock.Advance(idle)
	_, err = e.a.Refresh(ctx, is2.Tokens.Refresh, installID, iphone)
	refreshInvalid(t, err, "ровно на idle")
}

// Неизвестный, после выхода, у banned и pending_deletion — 401; суспендированный — обновляется.
func TestRefreshInvalidStates(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.a.Refresh(ctx, "неизвестный", installID, iphone)
	refreshInvalid(t, err, "неизвестный")
	_, is := loggedIn(t, e, "logged_out", iphone)
	if err := e.a.Logout(ctx, is.SessionID); err != nil {
		t.Fatal(err)
	}
	_, err = e.a.Refresh(ctx, is.Tokens.Refresh, installID, iphone)
	refreshInvalid(t, err, "после выхода")
	for _, st := range []string{"banned", "pending_deletion"} {
		uid, is := loggedIn(t, e, "st_"+st, iphone)
		setStatus(t, e, uid, st)
		_, err := e.a.Refresh(ctx, is.Tokens.Refresh, installID, iphone)
		refreshInvalid(t, err, st)
	}
	uid, is := loggedIn(t, e, "st_suspended", iphone)
	setStatus(t, e, uid, "suspended")
	if _, err := e.a.Refresh(ctx, is.Tokens.Refresh, installID, iphone); err != nil {
		t.Fatalf("suspended: %v", err)
	}
}

// Выход: revoked_reason = logout, событие sessions_revoked; повтор и чужой id — nil (идемпотентно), без события.
func TestLogout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, is := loggedIn(t, e, "bye", iphone)
	ctx := context.Background()
	if err := e.a.Logout(ctx, is.SessionID); err != nil {
		t.Fatal(err)
	}
	if r := revokedReason(t, e, is.SessionID); r != string(domain.RevokedLogout) {
		t.Fatalf("причина %q", r)
	}
	for _, sid := range []uuid.UUID{is.SessionID, uuid.New()} {
		if err := e.a.Logout(ctx, sid); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1`, identity.EventSessionsRevoked); n != 1 {
		t.Fatalf("событий %d", n)
	}
}
