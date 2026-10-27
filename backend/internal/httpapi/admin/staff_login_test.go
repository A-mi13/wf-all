package admin_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

const (
	pathLogin  = "/v1/staff/auth/login"
	pathMFA    = "/v1/staff/auth/mfa"
	pathUnlock = "/v1/staff/auth/unlock"
	pathLogout = "/v1/staff/auth/logout"
	pathAccept = "/v1/staff/auth/invite/accept"
	pathMe     = "/v1/staff/me"
)

// Вход (§4.6): пароль → mfa_pending (me — только стадия и сессия, сессия не null: экран входа сравнивает
// её id, Task 13 N2) → неверный код с attempts_left → верный → active с новым токеном. Резервный код
// принимается в любом регистре, с пробелами вместо дефиса, и сгорает. Пятая неудача подряд —
// attempts_left 0, сессии погашены (дальше 401 со стиранием cookie), вход закрыт.
func TestStaffLoginFlow(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	wantProblem(t, e.anon(pathLogin, "", e.creds(m.email, "не тот пароль")), http.StatusBadRequest, staff.CodeInvalidCredentials)
	wantProblem(t, e.anon(pathLogin, "", e.creds("nobody@wf.example", staffPassword)), http.StatusBadRequest, staff.CodeInvalidCredentials)

	rec := e.anon(pathLogin, "", e.creds(m.email, staffPassword))
	wantStatus(t, rec, http.StatusOK)
	wantNoStore(t, rec)
	if got := bodyOf(t, rec)["stage"]; got != auth.StageMFAPending {
		t.Fatalf("stage %v", got)
	}
	pending := newToken(t, rec)
	me := bodyOf(t, wantOK(t, e.get(pathMe, pending)))
	for _, k := range []string{"id", "email", "display_name", "locale", "role"} {
		if _, ok := me[k]; ok {
			t.Fatalf("в mfa_pending лишнее поле %s: %v", k, me)
		}
	}
	wantEmptyAccess(t, me)
	sess, _ := me["session"].(map[string]any)
	if me["stage"] != auth.StageMFAPending || sess == nil || sess["id"] == nil ||
		sess["expires_at"] != e.clock.Now().Add(5*time.Minute).Format(time.RFC3339) {
		t.Fatalf("me в mfa_pending: %v", me)
	}

	wantAttempts(t, e.post(pathMFA, pending, e.factor(e.wrongCode(m), "")), staff.CodeMFAInvalid, 4)
	rec = e.post(pathMFA, pending, e.factor(e.code(m), ""))
	wantStatus(t, rec, http.StatusNoContent)
	if len(setCookies(rec)) != 1 {
		t.Fatalf("Set-Cookie %q", setCookies(rec))
	}
	active := newToken(t, rec)
	me = bodyOf(t, wantOK(t, e.get(pathMe, active)))
	if me["stage"] != auth.StageActive || me["email"] != m.email || me["session"].(map[string]any)["id"] == sess["id"] {
		t.Fatalf("me после второго фактора: %v", me)
	}

	// резервный код как попало: заглавные, пробел вместо дефиса
	clearedCookie(t, e.post(pathLogout, active, ""))
	rc := strings.ToUpper(strings.ReplaceAll(m.codes[0], "-", " "))
	rec = e.post(pathMFA, e.login(m), e.factor("", rc))
	wantStatus(t, rec, http.StatusNoContent)
	newToken(t, rec)
	// сгоревший код — неудача; ещё четыре — вход закрыт
	pending = e.login(m)
	wantAttempts(t, e.post(pathMFA, pending, e.factor("", m.codes[0])), staff.CodeMFAInvalid, 4)
	for left := 3; left >= 0; left-- {
		wantAttempts(t, e.post(pathMFA, pending, e.factor(e.wrongCode(m), "")), staff.CodeMFAInvalid, left)
	}
	rec = e.get(pathMe, pending)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
	wantProblem(t, e.anon(pathLogin, "", e.creds(m.email, staffPassword)), http.StatusBadRequest, staff.CodeInvalidCredentials)
}

// D9: вход со старой живой cookie гасит прежнюю сессию (replaced): минуту её токен отвечает 401 без
// стирания (в браузере уже новая cookie), потом — 401 со стиранием.
func TestStaffLoginReplacesOldSession(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	rec := e.anon(pathLogin, m.token, e.creds(m.email, staffPassword))
	wantStatus(t, rec, http.StatusOK)
	if len(setCookies(rec)) != 1 {
		t.Fatalf("Set-Cookie %q", setCookies(rec))
	}
	newToken(t, rec)
	if r := e.sessionReason(m.token); r != "replaced" {
		t.Fatalf("прежняя сессия: %q", r)
	}
	rec = e.get(pathMe, m.token)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	noCookie(t, rec)
	e.clock.Advance(61 * time.Second)
	rec = e.get(pathMe, m.token)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
}

// Вход с недействительной старой cookie (⚠️ 9b): конвейер стирает её, хендлер ставит новую — два
// Set-Cookie, новая последней (браузер применяет по порядку).
func TestStaffLoginAfterInvalidCookie(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	rec := e.anon(pathLogin, "garbage-token", e.creds(m.email, staffPassword))
	wantStatus(t, rec, http.StatusOK)
	sc := setCookies(rec)
	if len(sc) != 2 || sc[0] != hostCookie+"=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict" {
		t.Fatalf("Set-Cookie %q", sc)
	}
	tok := newToken(t, rec)
	if bodyOf(t, wantOK(t, e.get(pathMe, tok)))["stage"] != auth.StageMFAPending {
		t.Fatal("новая cookie не работает")
	}
}

// D9: принятие приглашения передаёт сценарию токен cookie запроса — её живая сессия (чужая учётка
// тоже) гасится как replaced; ответ — одна новая cookie.
func TestStaffAcceptReplacesOldSession(t *testing.T) {
	e := newStaffEnv(t)
	owner := e.firstLogin("owner@wf.example", string(perm.Owner))
	e.invite("new@wf.example", string(perm.Support))
	rec := e.anon(pathAccept, owner.token, jsonOf(t, map[string]string{"email": "new@wf.example", "code": inviteCode, "password": staffPassword}))
	wantStatus(t, rec, http.StatusOK)
	if len(setCookies(rec)) != 1 {
		t.Fatalf("Set-Cookie %q", setCookies(rec))
	}
	newToken(t, rec)
	if r := e.sessionReason(owner.token); r != "replaced" {
		t.Fatalf("сессия старой cookie: %q", r)
	}
}

// Принятие: слабый пароль — validation.failed по body.password (до кода); неверный код — invite_invalid.
func TestStaffAcceptErrors(t *testing.T) {
	e := newStaffEnv(t)
	e.invite("new@wf.example", string(perm.Support))
	rec := e.anon(pathAccept, "", jsonOf(t, map[string]string{"email": "new@wf.example", "code": "aaaaa-aaaaa", "password": "short"}))
	wantField(t, rec, "body.password", "too_short")
	wantProblem(t, e.anon(pathAccept, "", jsonOf(t, map[string]string{"email": "new@wf.example", "code": "aaaaa-aaaaa", "password": staffPassword})),
		http.StatusBadRequest, staff.CodeInviteInvalid)
}

// Гонка смены токена (⚠️ 6a, 9b): запрос со старым токеном после второго фактора и после unlock —
// 401 без Set-Cookie: новая cookie в браузере не стирается.
func TestStaffReplacedTokenKeepsCookie(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	pending := e.login(m)
	rec := e.post(pathMFA, pending, e.factor(e.code(m), ""))
	wantStatus(t, rec, http.StatusNoContent)
	active := newToken(t, rec)
	for _, r := range []*httpRec{e.get(pathMe, pending), e.post(pathMFA, pending, e.factor(e.code(m), ""))} {
		wantProblem(t, r, http.StatusUnauthorized, httpx.CodeUnauthenticated)
		noCookie(t, r)
	}

	e.clock.Advance(21 * time.Minute)
	rec = e.post(pathUnlock, active, e.factor(e.code(m), ""))
	wantStatus(t, rec, http.StatusNoContent)
	unlocked := newToken(t, rec)
	rec = e.get("/v1/staff/me/sessions", active)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	noCookie(t, rec)
	wantOK(t, e.get("/v1/staff/me/sessions", unlocked))
}

// mfa_pending живёт 5 минут: за секунду до срока код принимается, ровно на сроке — 401 со стиранием
// cookie, сессия погашена как expired.
func TestStaffMFAPendingExpiresAtBoundary(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	p1 := e.login(m)
	e.clock.Advance(5*time.Minute - time.Second)
	wantStatus(t, e.post(pathMFA, p1, e.factor(totp(t, m.secret, e.clock.Now()), "")), http.StatusNoContent)

	p2 := e.login(m)
	e.clock.Advance(5 * time.Minute)
	rec := e.post(pathMFA, p2, e.factor(totp(t, m.secret, e.clock.Now()), ""))
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
	if r := e.sessionReason(p2); r != "expired" {
		t.Fatalf("сессия на сроке: %q", r)
	}
}

// Unlock после reauth_required (§4.7): me под Reauth отвечает (reauth_required: true), «своё» — 401
// reauth_required; неверный код — attempts_left; верный — новый токен, «своё» снова работает. Выход —
// 204 со стиранием cookie (и в стадии mfa_pending), дальше токен — 401 со стиранием.
func TestStaffUnlockAndLogout(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	e.clock.Advance(21 * time.Minute)
	if me := bodyOf(t, wantOK(t, e.get(pathMe, m.token))); me["reauth_required"] != true {
		t.Fatalf("me под Reauth: %v", me)
	}
	wantProblem(t, e.get("/v1/staff/me/sessions", m.token), http.StatusUnauthorized, httpx.CodeReauthRequired)
	wantAttempts(t, e.post(pathUnlock, m.token, e.factor(e.wrongCode(m), "")), staff.CodeMFAInvalid, 4)
	rec := e.post(pathUnlock, m.token, e.factor(e.code(m), ""))
	wantStatus(t, rec, http.StatusNoContent)
	tok := newToken(t, rec)
	wantOK(t, e.get("/v1/staff/me/sessions", tok))
	rec = e.post(pathLogout, tok, "")
	wantStatus(t, rec, http.StatusNoContent)
	clearedCookie(t, rec)
	rec = e.get(pathMe, tok)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)

	pending := e.login(m)
	rec = e.post(pathLogout, pending, "")
	wantStatus(t, rec, http.StatusNoContent)
	clearedCookie(t, rec)
	if r := e.sessionReason(pending); r != "logout" {
		t.Fatalf("выход в mfa_pending: %q", r)
	}
}

// Две вкладки разблокируют одним кодом (Review Focus 5): обе загружены конвейером до сценария (барьер
// в загрузчике), одна получает новый токен, другая — staff.mfa_invalid без записи неудачи; без 500.
func TestStaffUnlockTwoTabs(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	e.clock.Advance(21 * time.Minute)
	code := e.code(m)
	var loaded sync.WaitGroup
	loaded.Add(2)
	release := make(chan struct{})
	gate := func(*auth.Staff) {
		loaded.Done()
		<-release
	}
	e.onLoad.Store(&gate)
	var done sync.WaitGroup
	recs := make([]*httpRec, 2)
	for i := range recs {
		done.Go(func() { recs[i] = e.post(pathUnlock, m.token, e.factor(code, "")) })
	}
	loaded.Wait()
	e.onLoad.Store(nil)
	close(release)
	done.Wait()
	ok, failed := 0, 0
	for _, r := range recs {
		switch r.Code {
		case http.StatusNoContent:
			newToken(t, r)
			ok++
		case http.StatusBadRequest:
			wantAttempts(t, r, staff.CodeMFAInvalid, 5)
			noCookie(t, r)
			failed++
		default:
			t.Fatalf("ответ %d %s", r.Code, r.Body.String())
		}
	}
	if ok != 1 || failed != 1 {
		t.Fatalf("успехов %d, отказов %d", ok, failed)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.id); n != 0 {
		t.Fatalf("неудач записано %d", n)
	}
}

// Сессию погасили между конвейером и замком сценария (⚠️ 6a): 401 со стиранием cookie, как у конвейера;
// неудача второго фактора не пишется, другие сессии не тронуты.
func TestStaffSessionRevokedMidRequest(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	other := e.signIn(m)
	e.revokeOnLoad("revoked")
	rec := e.post("/v1/staff/me/sessions/revoke-others", m.token, "")
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
	e.onLoad.Store(nil)
	if r := e.sessionReason(other); r != "" {
		t.Fatalf("другая сессия: %q", r)
	}

	pending := e.login(m)
	code := e.code(m)
	e.revokeOnLoad("revoked")
	rec = e.post(pathMFA, pending, e.factor(code, ""))
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.id); n != 0 {
		t.Fatalf("неудач записано %d", n)
	}
}

// Сессию заменили между конвейером и замком (unlock в другой вкладке, ⚠️ 7, 9b): 401 без стирания
// cookie — в браузере уже новая; «завершить остальные» по старой сессии не гасит свежие.
func TestStaffSessionReplacedMidRequest(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	other := e.signIn(m)
	e.revokeOnLoad("replaced")
	rec := e.post("/v1/staff/me/sessions/revoke-others", m.token, "")
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	noCookie(t, rec)
	e.onLoad.Store(nil)
	if r := e.sessionReason(other); r != "" {
		t.Fatalf("другая сессия: %q", r)
	}

	// стадия setup (lockSetup): то же
	e.invite("new@wf.example", string(perm.Support))
	rec = e.anon(pathAccept, "", jsonOf(t, map[string]string{"email": "new@wf.example", "code": inviteCode, "password": staffPassword}))
	setup := newToken(t, rec)
	e.revokeOnLoad("replaced")
	rec = e.post("/v1/staff/me/mfa/setup", setup, "")
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	noCookie(t, rec)
}

// revokeOnLoad — после следующей загрузки сессии конвейером гасит её с причиной reason (гонка
// «между конвейером и замком сценария»); один раз.
func (e *staffEnv) revokeOnLoad(reason string) {
	f := func(s *auth.Staff) {
		e.onLoad.Store(nil)
		e.exec(`UPDATE staff_sessions SET revoked_at = $1, revoked_reason = $2 WHERE id = $3`, e.clock.Now(), reason, s.SessionID)
	}
	e.onLoad.Store(&f)
}

// wantField — 400 validation.failed по одному полю с правилом code.
func wantField(t *testing.T, rec *httpRec, field, code string) {
	t.Helper()
	wantProblem(t, rec, http.StatusBadRequest, httpx.CodeValidationFailed)
	errs, _ := bodyOf(t, rec)["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("поля: %s", rec.Body.String())
	}
	if f, _ := errs[0].(map[string]any); f["field"] != field || f["code"] != code {
		t.Fatalf("поле %v, ждали %s %s", f, field, code)
	}
}
