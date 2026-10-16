package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/staff"
)

const (
	pathSessions     = "/v1/staff/me/sessions"
	pathRevokeOthers = "/v1/staff/me/sessions/revoke-others"
	pathMePw         = "/v1/staff/me/password" //nolint:gosec // G101: путь ручки, не секрет
	pathLocale       = "/v1/staff/me/locale"
	pathSetup        = "/v1/staff/me/mfa/setup"
	pathConfirm      = "/v1/staff/me/mfa/confirm"
	pathAck          = "/v1/staff/me/mfa/recovery-codes/ack"
	pathRegenerate   = "/v1/staff/me/mfa/recovery-codes"
	pathResend       = "/v1/staff/auth/invite/resend"
	browserUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/141.0"
)

// pwBody — тело смены пароля; пустые code и recovery не передаются.
func pwBody(t *testing.T, current, code, recovery, next string) string {
	b := map[string]string{"current_password": current, "new_password": next}
	if code != "" {
		b["code"] = code
	}
	if recovery != "" {
		b["recovery_code"] = recovery
	}
	return jsonOf(t, b)
}

// Смена пароля (§4.7, D7): неверный текущий — staff.password_invalid с attempts_left (счётчик второго
// фактора); слабый новый — validation.failed по body.new_password; оба фактора сразу — по body.code;
// успех (резервным кодом) — 204, другие сессии погашены, текущая жива, вход — новым паролем.
func TestStaffChangePassword(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	other := e.signIn(m)
	wantAttempts(t, e.post(pathMePw, m.token, pwBody(t, "не тот пароль", e.code(m), "", newPassword)), staff.CodePasswordInvalid, 4)
	wantField(t, e.post(pathMePw, m.token, pwBody(t, staffPassword, e.code(m), "", "short")), "body.new_password", "too_short")
	wantField(t, e.post(pathMePw, m.token, pwBody(t, staffPassword, e.code(m), m.codes[1], newPassword)), "body.code", "oneOf")
	wantAttempts(t, e.post(pathMePw, m.token, pwBody(t, staffPassword, e.wrongCode(m), "", newPassword)), staff.CodeMFAInvalid, 3)

	wantStatus(t, e.post(pathMePw, m.token, pwBody(t, staffPassword, "", m.codes[1], newPassword)), http.StatusNoContent)
	rec := e.get(pathMe, other)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
	wantOK(t, e.get(pathMe, m.token))
	wantProblem(t, e.anon(pathLogin, "", e.creds(m.email, staffPassword)), http.StatusBadRequest, staff.CodeInvalidCredentials)
	wantStatus(t, e.anon(pathLogin, "", e.creds(m.email, newPassword)), http.StatusOK)
}

// Перевыпуск резервных кодов (§4.7, M9): неверный TOTP — attempts_left; верный — 10 новых кодов с
// no-store, прежние больше не действуют, новые — да.
func TestStaffRegenerateRecoveryCodes(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	wantAttempts(t, e.post(pathRegenerate, m.token, jsonOf(t, map[string]string{"code": e.wrongCode(m)})), staff.CodeMFAInvalid, 4)
	rec := e.post(pathRegenerate, m.token, jsonOf(t, map[string]string{"code": e.code(m)}))
	wantStatus(t, rec, http.StatusOK)
	wantNoStore(t, rec)
	codes, _ := bodyOf(t, rec)["recovery_codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("коды: %s", rec.Body.String())
	}
	wantAttempts(t, e.post(pathMFA, e.login(m), e.factor("", m.codes[0])), staff.CodeMFAInvalid, 4)
	wantStatus(t, e.post(pathMFA, e.login(m), e.factor("", codes[0].(string))), http.StatusNoContent)
	if mfa := bodyOf(t, wantOK(t, e.get("/v1/staff/me/mfa", m.token))); mfa["recovery_codes_left"] != float64(9) {
		t.Fatalf("mfa: %v", mfa)
	}
}

// Свои сессии (§4.7): живые, текущая отмечена, сеть — /24, пустой User-Agent — null (⚠️ 9a); чужой и
// несуществующий id — staff.session_not_found; своя другая — 204; «все другие» — число; текущая — 204
// без Set-Cookie, следующий запрос — 401 со стиранием от конвейера.
func TestStaffMySessions(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	rec := e.send(http.MethodPost, pathLogin, "", "", e.creds(m.email, staffPassword), "User-Agent", browserUA)
	rec = e.send(http.MethodPost, pathMFA, newToken(t, rec), uuid.NewString(), e.factor(e.code(m), ""), "User-Agent", browserUA)
	browser := newToken(t, rec)

	list := bodyOf(t, wantOK(t, e.get(pathSessions, m.token)))
	items, _ := list["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("сессии: %v", list)
	}
	byCurrent := map[bool]map[string]any{}
	for _, it := range items {
		s := it.(map[string]any)
		byCurrent[s["current"].(bool)] = s
		if s["ip"] != "192.0.2.0/24" || s["stage"] != "active" {
			t.Fatalf("сессия: %v", s)
		}
	}
	cur, oth := byCurrent[true], byCurrent[false]
	if cur == nil || oth == nil || cur["user_agent"] != nil || oth["user_agent"] != browserUA {
		t.Fatalf("сессии: %v", items)
	}
	if _, ok := cur["user_agent"]; !ok {
		t.Fatal("user_agent без ключа, ждали null")
	}

	y := e.firstLogin("y@wf.example", string(perm.Support))
	foreign := bodyOf(t, wantOK(t, e.get(pathMe, y.token)))["session"].(map[string]any)["id"].(string)
	for _, id := range []string{foreign, uuid.NewString()} {
		wantProblem(t, e.send(http.MethodDelete, pathSessions+"/"+id, m.token, uuid.NewString(), ""), http.StatusNotFound, staff.CodeSessionNotFound)
	}
	wantOK(t, e.get(pathMe, y.token))

	wantStatus(t, e.send(http.MethodDelete, pathSessions+"/"+oth["id"].(string), m.token, uuid.NewString(), ""), http.StatusNoContent)
	wantProblem(t, e.get(pathMe, browser), http.StatusUnauthorized, httpx.CodeUnauthenticated)

	third := e.signIn(m)
	if got := bodyOf(t, wantOK(t, e.post(pathRevokeOthers, m.token, "")))["revoked"]; got != float64(1) {
		t.Fatalf("revoked %v", got)
	}
	wantProblem(t, e.get(pathMe, third), http.StatusUnauthorized, httpx.CodeUnauthenticated)
	if got := bodyOf(t, wantOK(t, e.post(pathRevokeOthers, m.token, "")))["revoked"]; got != float64(0) {
		t.Fatalf("revoked %v", got)
	}

	rec = e.send(http.MethodDelete, pathSessions+"/"+cur["id"].(string), m.token, uuid.NewString(), "")
	wantStatus(t, rec, http.StatusNoContent)
	noCookie(t, rec)
	rec = e.get(pathMe, m.token)
	wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	clearedCookie(t, rec)
}

// Язык (R14): PATCH — 204 без тела, новый язык — в me; незнакомый — validation.failed по body.locale.
func TestStaffLocale(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	rec := e.send(http.MethodPatch, pathLocale, m.token, uuid.NewString(), `{"locale":"en"}`)
	wantStatus(t, rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Fatalf("тело 204: %q", rec.Body.String())
	}
	if got := bodyOf(t, wantOK(t, e.get(pathMe, m.token)))["locale"]; got != "en" {
		t.Fatalf("locale %v", got)
	}
	wantField(t, e.send(http.MethodPatch, pathLocale, m.token, uuid.NewString(), `{"locale":"de"}`), "body.locale", "enum")
}

// Review Focus 1, D14: ответы с секретами (секрет TOTP, коды при подтверждении и перевыпуске) не
// сохраняются в idempotency_keys — ни тело, ни заголовки; повтор с тем же ключом — 409
// idempotency.replay_unavailable с no-store. Строка ключа есть (статус), но ни секрета, ни кодов в
// таблице нет.
func TestStaffSecretsNotReplayed(t *testing.T) {
	e := newStaffEnv(t)
	e.invite("olga.v@wf.example", string(perm.Admin))
	setup := newToken(t, e.anon(pathAccept, "", jsonOf(t, map[string]string{"email": "olga.v@wf.example", "code": inviteCode, "password": staffPassword})))

	replayed := func(path, tok, body string) (*httpRec, string) {
		t.Helper()
		key := uuid.NewString()
		rec := e.send(http.MethodPost, path, tok, key, body)
		wantStatus(t, rec, http.StatusOK)
		wantNoStore(t, rec)
		again := e.send(http.MethodPost, path, tok, key, body)
		wantProblem(t, again, http.StatusConflict, httpx.CodeIdempotencyReplayUnavailable)
		wantNoStore(t, again)
		if n := e.count(`SELECT count(*) FROM idempotency_keys WHERE key = $1 AND response_status = 200
			AND response_body IS NULL AND response_headers IS NULL`, key); n != 1 {
			t.Fatalf("%s: строка ключа сохранила ответ", path)
		}
		return rec, key
	}
	rec, _ := replayed(pathSetup, setup, "")
	secret, _ := bodyOf(t, rec)["secret"].(string)
	uri, _ := bodyOf(t, rec)["otpauth_uri"].(string)
	rec, _ = replayed(pathConfirm, setup, jsonOf(t, map[string]string{"code": totp(t, secret, e.clock.Now())}))
	var secrets []string
	for _, c := range bodyOf(t, rec)["recovery_codes"].([]any) {
		secrets = append(secrets, c.(string))
	}
	ack := e.post(pathAck, setup, "")
	wantStatus(t, ack, http.StatusNoContent)
	active := newToken(t, ack)
	e.clock.Advance(30 * time.Second)
	rec, _ = replayed(pathRegenerate, active, jsonOf(t, map[string]string{"code": totp(t, secret, e.clock.Now())}))
	for _, c := range bodyOf(t, rec)["recovery_codes"].([]any) {
		secrets = append(secrets, c.(string))
	}
	secrets = append(secrets, secret, uri, setup, active)
	rows, err := e.owner.Query(context.Background(), `SELECT coalesce(response_body::text, '') || coalesce(response_headers::text, '') FROM idempotency_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		if containsAny([]byte(s), secrets...) {
			t.Fatalf("секрет в idempotency_keys: %s", s)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// Повтор второго фактора с тем же ключом после обрыва: Set-Cookie (новый токен) в idempotency_keys не
// сохраняется; прежний токен уже заменён — 401 без стирания cookie (новую повтор не вернёт: вход заново).
func TestStaffReplayAfterNewToken(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	pending := e.login(m)
	key, body := uuid.NewString(), e.factor(e.code(m), "")
	rec := e.send(http.MethodPost, pathMFA, pending, key, body)
	wantStatus(t, rec, http.StatusNoContent)
	active := newToken(t, rec)
	again := e.send(http.MethodPost, pathMFA, pending, key, body)
	wantProblem(t, again, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	noCookie(t, again)
	var headers string
	if err := e.owner.QueryRow(context.Background(), `SELECT coalesce(response_headers::text, '') FROM idempotency_keys
		WHERE key = $1 AND response_status = 204`, key).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if containsAny([]byte(headers), active, "Set-Cookie", hostCookie) {
		t.Fatalf("токен в idempotency_keys: %s", headers)
	}
}

// Повтор неудачного кода с тем же ключом — сохранённый 400 с тем же attempts_left, неудача не
// засчитывается второй раз.
func TestStaffFactorFailureReplay(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	pending := e.login(m)
	key, body := uuid.NewString(), e.factor(e.wrongCode(m), "")
	wantAttempts(t, e.send(http.MethodPost, pathMFA, pending, key, body), staff.CodeMFAInvalid, 4)
	again := e.send(http.MethodPost, pathMFA, pending, key, body)
	// сохранённое тело — jsonb: ключи и пробелы переставлены, сверка по полям
	if b := bodyOf(t, again); again.Code != http.StatusBadRequest || b["code"] != staff.CodeMFAInvalid || b["attempts_left"] != float64(4) {
		t.Fatalf("повтор: %d %v", again.Code, b)
	}
	if again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("ответ не из сохранённого")
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.id); n != 1 {
		t.Fatalf("неудач %d", n)
	}
}

// Самоотправка кода приглашения (§4.3 п.3, контракт — 202 всегда): раньше 60 с с прошлой отправки —
// 202 без письма (429 выдал бы существование почты); после паузы — 202 и задача staff.mail; сразу
// снова — без письма; несуществующая почта — 202.
func TestStaffResendInviteSelf(t *testing.T) {
	e := newStaffEnv(t)
	e.invite("new@wf.example", string(perm.Support))
	body := jsonOf(t, map[string]string{"email": "new@wf.example"})
	mails := func() int { return e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail'`) }
	rec := e.anon(pathResend, "", body)
	wantStatus(t, rec, http.StatusAccepted)
	if rec.Body.Len() != 0 || mails() != 0 {
		t.Fatalf("до паузы: тело %q, писем %d", rec.Body.String(), mails())
	}
	e.clock.Advance(61 * time.Second)
	wantStatus(t, e.anon(pathResend, "", body), http.StatusAccepted)
	wantStatus(t, e.anon(pathResend, "", body), http.StatusAccepted)
	wantStatus(t, e.anon(pathResend, "", jsonOf(t, map[string]string{"email": "nobody@wf.example"})), http.StatusAccepted)
	if n := mails(); n != 1 {
		t.Fatalf("писем %d", n)
	}
}

// Перебор самоотправки с одного адреса — 429 ratelimit.exceeded с Retry-After (IP-лимит staff_auth).
func TestStaffResendInviteSelfIPLimit(t *testing.T) {
	e := newStaffEnv(t, func(o *admin.Options) {
		rules := ratelimit.DefaultRules()
		r := rules["staff_auth"]
		r.IP = ratelimit.Policy{Limit: 1, Period: time.Minute, Burst: 1}
		rules["staff_auth"] = r
		o.RateRules, o.Limiter = rules, ratelimit.NewPG(o.DB, o.Clock)
	})
	body := jsonOf(t, map[string]string{"email": "new@wf.example"})
	wantStatus(t, e.anon(pathResend, "", body), http.StatusAccepted)
	rec := e.anon(pathResend, "", body)
	wantProblem(t, rec, http.StatusTooManyRequests, httpx.CodeRateLimited)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 без Retry-After")
	}
}

// Мутации «своё» (x-permission self) не доходят до сценариев не в стадии active (⚠️ 6b): setup — 403
// setup_required, mfa_pending — 401 unauthenticated, Reauth — 401 reauth_required.
func TestStaffSelfMutationsByStage(t *testing.T) {
	e := newStaffEnv(t)
	m := e.firstLogin("ivan.p@wf.example", string(perm.Owner))
	e.invite("new@wf.example", string(perm.Support))
	setup := newToken(t, e.anon(pathAccept, "", jsonOf(t, map[string]string{"email": "new@wf.example", "code": inviteCode, "password": staffPassword})))
	pending := e.login(m)
	mutations := func(tok string) []*httpRec {
		return []*httpRec{
			e.post(pathRevokeOthers, tok, ""),
			e.send(http.MethodPatch, pathLocale, tok, uuid.NewString(), `{"locale":"en"}`),
			e.post(pathMePw, tok, pwBody(t, staffPassword, "123456", "", newPassword)),
			e.post(pathRegenerate, tok, `{"code":"123456"}`),
			e.send(http.MethodDelete, pathSessions+"/"+uuid.NewString(), tok, uuid.NewString(), ""),
		}
	}
	for _, rec := range mutations(setup) {
		wantProblem(t, rec, http.StatusForbidden, httpx.CodeSetupRequired)
	}
	for _, rec := range mutations(pending) {
		wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	}
	e.clock.Advance(21 * time.Minute)
	for _, rec := range mutations(m.token) {
		wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeReauthRequired)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action IN ('staff.sessions_revoked', 'staff.locale_changed', 'staff.factor_failed')`); n != 0 {
		t.Fatalf("сценарии исполнились: %d записей", n)
	}
}
