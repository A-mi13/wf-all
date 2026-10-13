package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
	"wf/backend/internal/staff/internal/domain"
)

// pending — сессия mfa_pending после верного пароля.
func (e *env) pending(m member) *auth.Staff {
	e.t.Helper()
	return e.load(e.login(context.Background(), m))
}

// wantMFAInvalid — отказ второго фактора с attempts_left.
func wantMFAInvalid(t *testing.T, err error, left int) {
	t.Helper()
	me, ok := errors.AsType[*staff.MFAError](err)
	if !ok || !errors.Is(err, staff.ErrMFAInvalid) || me.AttemptsLeft != left {
		t.Fatalf("ждали staff.mfa_invalid, attempts_left %d; получили %v (%+v)", left, err, me)
	}
}

// Успех второго фактора: новый токен (стадия active), старый токен — «заменён»; mfa_at,
// last_login_at, счётчик неудач 0, аудит staff.login от сотрудника.
func TestVerifyMFASuccess(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{Name: "Ольга В."})
	e.exec(`UPDATE staff_accounts SET mfa_failures = 3 WHERE id = $1`, m.ID)
	ctx := context.Background()
	pendingTok := e.login(ctx, m)
	s := e.load(pendingTok)
	tok, err := e.app.VerifyMFA(ctx, s, e.totp(m), "", metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if tok == pendingTok {
		t.Fatal("токен не сменился")
	}
	active := e.load(tok)
	if active.Stage != auth.StageActive || active.Reauth || active.SessionID == s.SessionID || active.Role != "admin" ||
		len(active.Grants) == 0 {
		t.Fatalf("сессия %+v", active)
	}
	if _, err := e.app.LoadStaff(ctx, pendingTok, ipHome, false); !errors.Is(err, auth.ErrSessionReplaced) {
		t.Fatalf("старый токен: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND mfa_failures = 0 AND last_login_at = $2`, m.ID, t0); n != 1 {
		t.Fatal("счётчик или last_login_at")
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1 AND mfa_at = $2 AND expires_at = $3`,
		active.SessionID, t0, t0.Add(8*time.Hour)); n != 1 {
		t.Fatal("mfa_at или срок 8 ч")
	}
	rows := e.audits("staff.login")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != m.ID || rows[0].Role != "admin" ||
		rows[0].Label == nil || *rows[0].Label != "Ольга В." {
		t.Fatalf("аудит входа %+v", rows)
	}
	// второй фактор только в mfa_pending
	if _, err := e.app.VerifyMFA(ctx, active, e.totp(m), "", metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("VerifyMFA в active: %v", err)
	}
}

// Review Focus 5: код предыдущего шага принимается один раз; тот же код вторично и код старше
// принятого отвергаются.
func TestTOTPPreviousStepOnce(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	ctx := context.Background()
	prev := domain.TOTPCode(m.Secret, domain.TOTPStep(e.clock.Now())-1)
	if _, err := e.app.VerifyMFA(ctx, e.pending(m), prev, "", metaWeb); err != nil {
		t.Fatalf("код предыдущего шага: %v", err)
	}
	_, err := e.app.VerifyMFA(ctx, e.pending(m), prev, "", metaWeb)
	wantMFAInvalid(t, err, 4)
	older := domain.TOTPCode(m.Secret, domain.TOTPStep(e.clock.Now())-1)
	e.clock.Advance(30 * time.Second) // тот же код — теперь «два шага назад»: вне окна
	_, err = e.app.VerifyMFA(ctx, e.pending(m), older, "", metaWeb)
	wantMFAInvalid(t, err, 3)
	// текущий код нового шага — принимается
	if _, err := e.app.VerifyMFA(ctx, e.pending(m), e.totp(m), "", metaWeb); err != nil {
		t.Fatalf("текущий код: %v", err)
	}
}

// Review Focus 5: резервный код строчными, без дефиса, с пробелами и \r\n принимается и сгорает —
// второй раз тот же код (в любом виде) не подходит.
func TestLoginRecoveryCodeOnce(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	ctx := context.Background()
	for i, variant := range []func(string) string{
		func(c string) string { return strings.ToUpper(c) },
		func(c string) string { return strings.ReplaceAll(c, "-", "") },
		func(c string) string { return " " + c[:3] + " " + c[3:] + " \r\n" },
		func(c string) string { return strings.ToUpper(strings.ReplaceAll(c, "-", "")) + "\r\n" },
	} {
		code := variant(m.Codes[i])
		if _, err := e.app.VerifyMFA(ctx, e.pending(m), "", code, metaWeb); err != nil {
			t.Fatalf("вариант %d %q: %v", i, code, err)
		}
		_, err := e.app.VerifyMFA(ctx, e.pending(m), "", m.Codes[i], metaWeb)
		wantMFAInvalid(t, err, 4)
		e.exec(`UPDATE staff_accounts SET mfa_failures = 0 WHERE id = $1`, m.ID)
	}
	if n := e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NOT NULL`, m.ID); n != 4 {
		t.Fatalf("сгоревших кодов %d", n)
	}
	// чужой код и мусор — неудача
	_, err := e.app.VerifyMFA(ctx, e.pending(m), "", "aaaaa-aaaaa", metaWeb)
	wantMFAInvalid(t, err, 4)
	_, err = e.app.VerifyMFA(ctx, e.pending(m), "", "не код", metaWeb)
	wantMFAInvalid(t, err, 3)
}

// Блокировка (§4.6, D7): неудачи второго фактора подряд — attempts_left 4…0; пятая ставит
// locked_until = now + 1 ч, гасит все сессии (locked), публикует staff.locked; письмо владельцам и
// админам — одно в час на учётку. Аудит неудач — от учётки (шаг mfa — staff.login_failed),
// блокировка — от системы. Пока закрыта — вход отвечает invalid_credentials.
func TestLockoutAfterFiveFailures(t *testing.T) {
	e := newEnv(t)
	e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	e.addMember("admin2@wf.example", memberOpts{Role: "admin"})
	e.addMember("support@wf.example", memberOpts{Role: "support"})
	e.addMember("off@wf.example", memberOpts{Role: "owner", Status: "disabled"})
	m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Name: "Модератор", Cities: []uuid.UUID{e.city("kazan")}})
	ctx := context.Background()
	other := e.load(e.signIn(m)) // вторая живая сессия того же сотрудника
	s := e.pending(m)
	for want := 4; want >= 0; want-- {
		_, err := e.app.VerifyMFA(ctx, s, "000000", "", metaWeb)
		wantMFAInvalid(t, err, want)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND locked_until = $2 AND lock_notified_at = $3
		AND mfa_failures = 5`, m.ID, t0.Add(time.Hour), t0); n != 1 {
		t.Fatal("учётка не закрыта на час")
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE staff_id = $1 AND (revoked_reason IS DISTINCT FROM 'locked')
		AND revoked_reason IS DISTINCT FROM 'replaced'`, m.ID); n != 0 {
		t.Fatalf("живых сессий после блокировки %d", n)
	}
	if _, err := e.app.Unlock(ctx, other, "000000", "", metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("сессия после блокировки: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'staff.locked' AND aggregate_id = $1`, m.ID); n != 1 {
		t.Fatalf("событий staff.locked %d", n)
	}
	// письма: активные владелец и админ — да; поддержка, выключенный владелец — нет
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail' AND queue = 'staff'
		AND args->>'kind' = 'staff.locked' AND args->>'ref' = $1::text`, s.SessionID); n != 2 {
		t.Fatalf("писем о блокировке %d, ждали 2", n)
	}
	failed := e.audits("staff.login_failed")
	if len(failed) != 5 {
		t.Fatalf("неудач в аудите %d", len(failed))
	}
	for _, r := range failed {
		if r.Role != "moderator" || r.StaffID == nil || *r.StaffID != m.ID || r.Label == nil || *r.Label != "Модератор" ||
			!strings.Contains(r.After, `"step": "mfa"`) {
			t.Fatalf("неудача второго фактора не от учётки: %+v", r)
		}
	}
	if !strings.Contains(failed[4].After, `"attempts_left": 0`) {
		t.Fatalf("последняя неудача %s", failed[4].After)
	}
	lock := e.audits("staff.locked")
	if len(lock) != 1 || lock[0].Role != "system" || lock[0].StaffID != nil || lock[0].ObjectID == nil || *lock[0].ObjectID != m.ID {
		t.Fatalf("аудит блокировки %+v", lock)
	}
	// пока закрыта — верный пароль не пускает
	if _, err := e.app.Login(ctx, m.Email, testPassword, metaWeb); !errors.Is(err, staff.ErrInvalidCredentials) {
		t.Fatalf("вход в закрытую учётку: %v", err)
	}

	// через час — вход открыт; новая неудача снова закрывает (счётчик сбрасывает только успех),
	// но письмо не уходит повторно раньше, чем через час после прошлого
	e.clock.Advance(time.Hour)
	s = e.pending(m)
	_, err := e.app.VerifyMFA(ctx, s, "000000", "", metaWeb)
	wantMFAInvalid(t, err, 0)
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail'`); n != 4 {
		t.Fatalf("писем %d: письмо о повторной блокировке через час — ждали ещё 2", n)
	}
	e.clock.Advance(59 * time.Minute)
	e.exec(`UPDATE staff_accounts SET locked_until = NULL WHERE id = $1`, m.ID)
	s = e.pending(m)
	_, err = e.app.VerifyMFA(ctx, s, "000000", "", metaWeb)
	wantMFAInvalid(t, err, 0)
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail'`); n != 4 {
		t.Fatalf("писем %d: раньше часа после прошлого письма новых быть не должно", n)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'staff.locked'`); n != 3 {
		t.Fatalf("событий %d: каждая блокировка — событие", n)
	}
}

// D8: запись неудачи переживает откат основной транзакции — бизнес-ошибка (4xx) не откатывает
// счётчик и аудит. Под Idempotency-Key это и есть единственная закоммиченная транзакция запроса:
// хук идемпотентности видит откат проверки и коммит записи неудачи.
func TestFailureSurvivesRollback(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.pending(m)
	var outcomes []bool
	ctx := db.WithTxHook(context.Background(), func(context.Context, pgxTx) (func(bool), error) {
		return func(committed bool) { outcomes = append(outcomes, committed) }, nil
	})
	_, err := e.app.VerifyMFA(ctx, s, "000000", "", metaWeb)
	wantMFAInvalid(t, err, 4)
	if len(outcomes) != 2 || outcomes[0] || !outcomes[1] {
		t.Fatalf("исходы транзакций %v, ждали [откат проверки, коммит неудачи]", outcomes)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 1 {
		t.Fatalf("счётчик %d", n)
	}
	if len(e.audits("staff.login_failed")) != 1 {
		t.Fatal("аудит неудачи откатился")
	}
	// успех сбрасывает счётчик
	if _, err := e.app.VerifyMFA(context.Background(), s, e.totp(m), "", metaWeb); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик после успеха %d", n)
	}
}

// Неудачи unlock — staff.factor_failed от учётки и тот же счётчик блокировки.
func TestUnlockFailureAudit(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{Role: "support", Name: "Поддержка"})
	s := e.load(e.signIn(m))
	_, err := e.app.Unlock(context.Background(), s, "000000", "", metaWeb)
	wantMFAInvalid(t, err, app.MaxFactorFailures-1)
	rows := e.audits("staff.factor_failed")
	if len(rows) != 1 || rows[0].Role != "support" || rows[0].StaffID == nil || *rows[0].StaffID != m.ID ||
		!strings.Contains(rows[0].After, `"step": "unlock"`) {
		t.Fatalf("аудит %+v", rows)
	}
	if len(e.audits("staff.login_failed")) != 0 {
		t.Fatal("неудача unlock записана как неудача входа")
	}
}

// Review Focus 5: две вкладки разблокируют одним кодом — один успех и staff.mfa_invalid, без 500;
// вторая неудачей не считается (это не перебор).
func TestUnlockConcurrentSameCode(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	tok := e.signIn(m)
	e.clock.Advance(21 * time.Minute)
	s, err := e.app.LoadStaff(context.Background(), tok, ipHome, false)
	if err != nil || !s.Reauth {
		t.Fatalf("Reauth: %+v %v", s, err)
	}
	e.clock.Advance(30 * time.Second) // новый шаг TOTP: код входа уже использован
	code := e.totp(m)
	var (
		wg   sync.WaitGroup
		errs [2]error
		toks [2]string
	)
	for i := range 2 {
		wg.Go(func() { toks[i], errs[i] = e.app.Unlock(context.Background(), s, code, "", metaWeb) })
	}
	wg.Wait()
	ok, invalid := 0, 0
	for i, err := range errs {
		switch {
		case err == nil && toks[i] != "":
			ok++
		case errors.Is(err, staff.ErrMFAInvalid):
			invalid++
		default:
			t.Fatalf("вкладка %d: %v", i, err)
		}
	}
	if ok != 1 || invalid != 1 {
		t.Fatalf("успехов %d, отказов %d", ok, invalid)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("вторая вкладка посчитана неудачей: %d", n)
	}
}

// Гонка смены токена (находка ревью 9b): после Unlock старый токен минуту отвечает
// auth.ErrSessionReplaced (конвейер не стирает cookie), потом — ErrSessionInvalid. Новый токен —
// active без Reauth, абсолютный срок сессии — от входа, IP — новый.
func TestUnlockReplacesToken(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	oldTok := e.signIn(m)
	e.clock.Advance(30 * time.Second)
	moved := netip4("198.51.100.7")
	s, err := e.app.LoadStaff(context.Background(), oldTok, moved, false)
	if err != nil || !s.Reauth {
		t.Fatalf("смена сети: %+v %v", s, err)
	}
	newTok, err := e.app.Unlock(context.Background(), s, e.totp(m), "", staffMeta(moved))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []time.Duration{0, app.ReplacedGrace - time.Second} {
		e.clock.Set(t0.Add(30*time.Second + d))
		if _, err := e.app.LoadStaff(context.Background(), oldTok, moved, false); !errors.Is(err, auth.ErrSessionReplaced) {
			t.Fatalf("старый токен через %v: %v", d, err)
		}
	}
	e.clock.Set(t0.Add(30*time.Second + app.ReplacedGrace))
	if _, err := e.app.LoadStaff(context.Background(), oldTok, moved, false); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("старый токен после льготы: %v", err)
	}
	ns, err := e.app.LoadStaff(context.Background(), newTok, moved, false)
	if err != nil || ns.Reauth || ns.Stage != auth.StageActive {
		t.Fatalf("новая сессия %+v %v", ns, err)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1 AND ip = '198.51.100.7' AND mfa_at = $2
		AND expires_at = $3`, ns.SessionID, t0.Add(30*time.Second), t0.Add(8*time.Hour)); n != 1 {
		t.Fatal("новая сессия: IP, mfa_at или абсолютный срок")
	}
	if len(e.audits("staff.session_unlocked")) != 1 {
		t.Fatal("нет аудита разблокировки")
	}
	// Unlock — только в стадии active
	if _, err := e.app.Unlock(context.Background(), e.pending(m), e.totp(m), "", metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("Unlock в mfa_pending: %v", err)
	}
}
