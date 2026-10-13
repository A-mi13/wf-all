package app_test

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/token"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

const newPassword = "Свисток на 46-й минуте" //nolint:gosec // G101: новый пароль тестовой учётки

// rawSession — сессия сотрудника прямо в базе (другие устройства, стадия setup): стадия, адрес,
// User-Agent как есть и время входа. Возвращает токен и id.
func (e *env) rawSession(staffID uuid.UUID, stage string, ip netip.Addr, ua string, at time.Time) (string, uuid.UUID) {
	e.t.Helper()
	raw, hash, err := token.NewRefresh()
	if err != nil {
		e.t.Fatal(err)
	}
	sid := id.New()
	ttl := 8 * time.Hour
	var mfaAt *time.Time
	switch stage {
	case auth.StageActive:
		mfaAt = &at
	case auth.StageMFAPending:
		ttl = 5 * time.Minute
	}
	e.exec(`INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, mfa_at, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4::staff_session_stage, $5, $5, $6, $7, $8, $9)`, sid, staffID, hash, stage, at, mfaAt, at.Add(ttl), ip, ua)
	return raw, sid
}

// revokedReason — причина гашения сессии (” — жива).
func (e *env) revokedReason(sid uuid.UUID) string {
	e.t.Helper()
	var r *string
	if err := e.owner.QueryRow(context.Background(), `SELECT revoked_reason FROM staff_sessions WHERE id = $1`, sid).Scan(&r); err != nil {
		e.t.Fatal(err)
	}
	if r == nil {
		return ""
	}
	return *r
}

// wantAuditMeta — у всех записей аудита action (а их n > 0) адрес и User-Agent запроса (metaWeb).
func (e *env) wantAuditMeta(action string) {
	e.t.Helper()
	all := e.count(`SELECT count(*) FROM audit_log WHERE action = $1`, action)
	withMeta := e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND ip = $2 AND user_agent = $3`,
		action, metaWeb.IP, metaWeb.UserAgent)
	if all == 0 || withMeta != all {
		e.t.Fatalf("%s: записей %d, с IP и User-Agent запроса %d", action, all, withMeta)
	}
}

// wantFieldError — 400 validation.failed с одним полем field и правилом code.
func wantFieldError(t *testing.T, err error, field, code string) {
	t.Helper()
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !ok {
		t.Fatalf("ждали validation.failed %s/%s, получили %v", field, code, err)
	}
	p := pe.Problem()
	if p.Code != httpx.CodeValidationFailed || len(p.Errors) != 1 || p.Errors[0] != (httpx.FieldError{Field: field, Code: code}) {
		t.Fatalf("ждали %s/%s, получили %+v", field, code, p)
	}
}

// wantFactorFailure — неудача второго фактора вида kind с attempts_left.
func wantFactorFailure(t *testing.T, err, kind error, left int) {
	t.Helper()
	me, ok := errors.AsType[*staff.MFAError](err)
	if !ok || !errors.Is(err, kind) || me.AttemptsLeft != left {
		t.Fatalf("ждали %v, attempts_left %d; получили %v (%+v)", kind, left, err, me)
	}
}

// R15: активная сессия — учётка, роль, города модератора, права роли и текущая сессия.
func TestMeActive(t *testing.T) {
	e := newEnv(t)
	c1, c2 := e.city("kazan"), e.city("perm")
	m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Name: "Ольга В.", Locale: "en", Cities: []uuid.UUID{c1, c2}})
	s := e.load(e.signIn(m))
	me, err := e.app.Me(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	want := staff.MeAccount{ID: m.ID, Email: m.Email, DisplayName: "Ольга В.", Locale: "en", Role: "moderator"}
	if me.Account == nil || *me.Account != want {
		t.Fatalf("учётка %+v", me.Account)
	}
	cities := []uuid.UUID{c1, c2}
	slices.SortFunc(cities, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if !slices.Equal(me.CityIDs, cities) || me.Stage != auth.StageActive || me.ReauthRequired {
		t.Fatalf("me %+v", me)
	}
	if !maps.Equal(me.Permissions, perm.Grants(perm.Moderator)) || len(me.Permissions) == 0 {
		t.Fatalf("права %v", me.Permissions)
	}
	if me.Session == nil || me.Session.ID != s.SessionID || !me.Session.ExpiresAt.Equal(t0.Add(8*time.Hour)) {
		t.Fatalf("сессия %+v", me.Session)
	}
	// права — копия: правка ответа не трогает сотрудника запроса
	clear(me.Permissions)
	if len(s.Grants) == 0 {
		t.Fatal("Me отдал карту прав сессии, а не копию")
	}

	// глобальная роль — пустой список городов, не nil (в JSON — [])
	owner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	me, err = e.app.Me(context.Background(), e.load(e.signIn(owner)))
	if err != nil {
		t.Fatal(err)
	}
	if me.CityIDs == nil || len(me.CityIDs) != 0 || !maps.Equal(me.Permissions, perm.Grants(perm.Owner)) {
		t.Fatalf("владелец %+v", me)
	}
}

// P20: в mfa_pending — только стадия и сессия; учётки, ролей, городов и прав нет, списки пустые.
func TestMeMFAPending(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{e.city("kazan")}})
	s := e.pending(m)
	me, err := e.app.Me(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if me.Account != nil || me.CityIDs == nil || len(me.CityIDs) != 0 || me.Permissions == nil || len(me.Permissions) != 0 {
		t.Fatalf("mfa_pending раскрыл учётку: %+v", me)
	}
	if me.Stage != auth.StageMFAPending || me.ReauthRequired {
		t.Fatalf("стадия %+v", me)
	}
	if me.Session == nil || me.Session.ID != s.SessionID || !me.Session.ExpiresAt.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("сессия %+v", me.Session)
	}
}

// R15: в setup — учётка без прав.
func TestMeSetup(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("new@wf.example", memberOpts{Role: "admin", Status: "invited", NoMFA: true, Name: "Новый"})
	tok, sid := e.rawSession(m.ID, auth.StageSetup, ipHome, metaWeb.UserAgent, t0)
	s := e.load(tok)
	if len(s.Grants) == 0 {
		t.Fatal("загрузчик не дал прав роли — тест проверяет вхолостую")
	}
	me, err := e.app.Me(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if me.Account == nil || me.Account.ID != m.ID || me.Account.Role != "admin" || me.Account.DisplayName != "Новый" {
		t.Fatalf("учётка %+v", me.Account)
	}
	if me.Permissions == nil || len(me.Permissions) != 0 || me.Stage != auth.StageSetup {
		t.Fatalf("setup с правами: %+v", me)
	}
	if me.Session == nil || me.Session.ID != sid {
		t.Fatalf("сессия %+v", me.Session)
	}
}

// P20: Reauth (бездействие) — reauth_required, остальное как у активной сессии.
func TestMeReauth(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	tok := e.signIn(m)
	e.clock.Advance(21 * time.Minute)
	s := e.load(tok)
	if !s.Reauth {
		t.Fatal("нет Reauth — тест проверяет вхолостую")
	}
	me, err := e.app.Me(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if !me.ReauthRequired || me.Account == nil || me.Stage != auth.StageActive {
		t.Fatalf("me %+v", me)
	}
}

// «Мой доступ»: только свои живые сессии, новые сверху; текущая отмечена; IP — подсеть /24 или /48;
// пустой User-Agent — nil.
func TestMySessions(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	other := e.addMember("ivan@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	_, phone := e.rawSession(m.ID, auth.StageActive, netip.MustParseAddr("2001:db8:1:2::5"), "", t0.Add(-time.Hour))
	_, gone := e.rawSession(m.ID, auth.StageActive, ipHome, "x", t0.Add(-2*time.Hour))
	e.exec(`UPDATE staff_sessions SET revoked_at = $2, revoked_reason = 'logout' WHERE id = $1`, gone, t0)
	e.rawSession(m.ID, auth.StageActive, ipHome, "x", t0.Add(-9*time.Hour)) // истекла, но не погашена
	e.rawSession(other.ID, auth.StageActive, ipHome, "x", t0.Add(-time.Minute))

	got, err := e.app.MySessions(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != s.SessionID || got[1].ID != phone {
		t.Fatalf("сессии %+v", got)
	}
	cur, ph := got[0], got[1]
	if !cur.Current || ph.Current {
		t.Fatalf("отметка текущей: %+v / %+v", cur, ph)
	}
	if cur.IP != "203.0.113.0/24" || ph.IP != "2001:db8:1::/48" {
		t.Fatalf("адреса %q, %q — не маскированы", cur.IP, ph.IP)
	}
	if cur.UserAgent == nil || *cur.UserAgent != metaWeb.UserAgent || ph.UserAgent != nil {
		t.Fatalf("User-Agent %v / %v", cur.UserAgent, ph.UserAgent)
	}
	if cur.Stage != auth.StageActive || !cur.CreatedAt.Equal(t0) || !cur.LastSeenAt.Equal(t0) ||
		!cur.ExpiresAt.Equal(t0.Add(8*time.Hour)) || !ph.CreatedAt.Equal(t0.Add(-time.Hour)) {
		t.Fatalf("поля сессии %+v", cur)
	}
}

// Своя сессия гасится (revoked) и пишется в аудит; чужая, несуществующая и уже погашенная —
// staff.session_not_found без различия; текущую тоже можно (контракт) — дальше сессии нет.
func TestRevokeMySession(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{Name: "Ольга В."})
	other := e.addMember("ivan@wf.example", memberOpts{})
	tok := e.signIn(m)
	s := e.load(tok)
	_, laptop := e.rawSession(m.ID, auth.StageActive, ipHome, "x", t0.Add(-time.Hour))
	_, foreign := e.rawSession(other.ID, auth.StageActive, ipHome, "x", t0.Add(-time.Hour))
	ctx := context.Background()

	if err := e.app.RevokeMySession(ctx, s, laptop, metaWeb); err != nil {
		t.Fatal(err)
	}
	if r := e.revokedReason(laptop); r != "revoked" {
		t.Fatalf("причина гашения %q", r)
	}
	rows := e.audits("staff.session_revoked")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != m.ID || rows[0].Role != "admin" ||
		rows[0].Label == nil || *rows[0].Label != "Ольга В." || !strings.Contains(rows[0].After, laptop.String()) {
		t.Fatalf("аудит %+v", rows)
	}
	e.wantAuditMeta("staff.session_revoked")
	for name, sid := range map[string]uuid.UUID{"чужая": foreign, "несуществующая": id.New(), "погашенная": laptop} {
		if err := e.app.RevokeMySession(ctx, s, sid, metaWeb); !errors.Is(err, staff.ErrSessionNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if r := e.revokedReason(foreign); r != "" {
		t.Fatalf("чужая сессия погашена: %q", r)
	}
	if n := len(e.audits("staff.session_revoked")); n != 1 {
		t.Fatalf("записей аудита %d — отказ записан", n)
	}
	if err := e.app.RevokeMySession(ctx, s, s.SessionID, metaWeb); err != nil {
		t.Fatalf("текущая: %v", err)
	}
	if _, err := e.app.LoadStaff(ctx, tok, ipHome, false); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("текущая после гашения: %v", err)
	}
}

// «Завершить остальные»: счёт погашенных, текущая жива, чужие не тронуты; повтор — 0.
func TestRevokeOtherSessions(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	other := e.addMember("ivan@wf.example", memberOpts{})
	tok := e.signIn(m)
	s := e.load(tok)
	_, a := e.rawSession(m.ID, auth.StageActive, ipHome, "x", t0.Add(-time.Hour))
	_, b := e.rawSession(m.ID, auth.StageMFAPending, ipHome, "x", t0.Add(-time.Minute))
	_, foreign := e.rawSession(other.ID, auth.StageActive, ipHome, "x", t0.Add(-time.Hour))
	ctx := context.Background()

	n, err := e.app.RevokeOtherSessions(ctx, s, metaWeb)
	if err != nil || n != 2 {
		t.Fatalf("погашено %d: %v", n, err)
	}
	if e.revokedReason(a) != "revoked" || e.revokedReason(b) != "revoked" || e.revokedReason(foreign) != "" {
		t.Fatal("погашены не те сессии")
	}
	e.load(tok) // текущая жива
	rows := e.audits("staff.sessions_revoked")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != m.ID || !strings.Contains(rows[0].After, `"count": 2`) {
		t.Fatalf("аудит %+v", rows)
	}
	e.wantAuditMeta("staff.sessions_revoked")
	if n, err := e.app.RevokeOtherSessions(ctx, s, metaWeb); err != nil || n != 0 {
		t.Fatalf("повтор: %d, %v", n, err)
	}
	if n := len(e.audits("staff.sessions_revoked")); n != 1 {
		t.Fatalf("записей аудита %d — пустое гашение записано", n)
	}
}

// Гонка смены токена: сотрудник запроса загружен со старым токеном, а unlock в другой вкладке уже
// заменил сессию. «Завершить остальные» не гасит свежую сессию (она для старой «другая»), а отвечает
// auth.ErrSessionReplaced (401 без стирания новой cookie); погашенная сессия — ErrSessionInvalid. Так
// же ведут себя «завершить сессию» и смена языка: ничего не меняют и ничего не пишут.
func TestSelfMutationsRecheckSession(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	_, laptop := e.rawSession(m.ID, auth.StageActive, ipHome, "x", t0.Add(-time.Hour))
	s := e.load(e.signIn(m))
	ctx := context.Background()
	e.clock.Advance(30 * time.Second) // код входа уже принят — следующий шаг
	fresh, err := e.app.Unlock(ctx, s, e.totp(m), "", metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.RevokeOtherSessions(ctx, s, metaWeb); !errors.Is(err, auth.ErrSessionReplaced) {
		t.Fatalf("завершить остальные по заменённой сессии: %v", err)
	}
	e.load(fresh) // свежая сессия жива
	if err := e.app.RevokeMySession(ctx, s, laptop, metaWeb); !errors.Is(err, auth.ErrSessionReplaced) {
		t.Fatalf("завершить сессию по заменённой сессии: %v", err)
	}
	if err := e.app.SetLocale(ctx, s, "en", metaWeb); !errors.Is(err, auth.ErrSessionReplaced) {
		t.Fatalf("язык по заменённой сессии: %v", err)
	}

	cur := e.load(fresh)
	if err := e.app.Logout(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.RevokeOtherSessions(ctx, cur, metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("завершить остальные по погашенной сессии: %v", err)
	}
	if err := e.app.RevokeMySession(ctx, cur, laptop, metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("завершить сессию по погашенной сессии: %v", err)
	}
	if err := e.app.SetLocale(ctx, cur, "en", metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("язык по погашенной сессии: %v", err)
	}
	if r := e.revokedReason(laptop); r != "" {
		t.Fatalf("сессия ноутбука погашена: %q", r)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND locale = 'ru'`, m.ID); n != 1 {
		t.Fatal("язык сменился по недействительной сессии")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action IN ('staff.session_revoked', 'staff.sessions_revoked',
		'staff.locale_changed')`); n != 0 {
		t.Fatalf("записей аудита %d", n)
	}
}

// withFactor засчитывает неудачей только staff.ErrMFAInvalid и staff.ErrPasswordInvalid: иной «вид
// неудачи» от check — ошибка без записи неудачи (откат), а не MFAError.
func TestWithFactorRejectsUnknownFailKind(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	odd := errors.New("не вид неудачи")
	err := e.app.FactorWith(context.Background(), s, "password_change", metaWeb, odd)
	if !errors.Is(err, odd) {
		t.Fatalf("ждали ошибку как есть, получили %v", err)
	}
	if _, ok := errors.AsType[*staff.MFAError](err); ok {
		t.Fatalf("неизвестный вид засчитан неудачей: %v", err)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик %d", n)
	}
	if n := len(e.audits("staff.factor_failed")); n != 0 {
		t.Fatalf("неудач в аудите %d", n)
	}
	// известный вид — неудача, как раньше
	err = e.app.FactorWith(context.Background(), s, "password_change", metaWeb, staff.ErrMFAInvalid)
	wantFactorFailure(t, err, staff.ErrMFAInvalid, app.MaxFactorFailures-1)
}

// §4.7, M9: смена пароля — текущий пароль, второй фактор и новый пароль; успех гасит другие сессии
// (password_changed), текущая жива, резервный код сгорает, счётчик неудач — 0; ровно одна
// закоммиченная транзакция; аудит staff.password_changed.
func TestChangePasswordSuccess(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	tok := e.signIn(m)
	s := e.load(tok)
	_, laptop := e.rawSession(m.ID, auth.StageActive, ipHome, "x", t0.Add(-time.Hour))
	e.exec(`UPDATE staff_accounts SET mfa_failures = 2 WHERE id = $1`, m.ID)
	var outcomes []bool
	ctx := db.WithTxHook(context.Background(), func(context.Context, pgxTx) (func(bool), error) {
		return func(committed bool) { outcomes = append(outcomes, committed) }, nil
	})

	if err := e.app.ChangePassword(ctx, s, testPassword, "", m.Codes[3], newPassword, metaWeb); err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0] {
		t.Fatalf("исходы транзакций %v, ждали один коммит", outcomes)
	}
	if r := e.revokedReason(laptop); r != "password_changed" {
		t.Fatalf("другая сессия: %q", r)
	}
	e.load(tok) // текущая жива
	if n := e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NOT NULL`, m.ID); n != 1 {
		t.Fatalf("сгоревших кодов %d", n)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик %d", n)
	}
	rows := e.audits("staff.password_changed")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != m.ID ||
		!strings.Contains(rows[0].After, `"method": "recovery"`) || !strings.Contains(rows[0].After, `"sessions_revoked": 1`) {
		t.Fatalf("аудит %+v", rows)
	}
	e.wantAuditMeta("staff.password_changed")
	// новый пароль входит, старый — нет; резервный код второй раз не подходит
	if _, err := e.app.Login(context.Background(), m.Email, testPassword, metaWeb); !errors.Is(err, staff.ErrInvalidCredentials) {
		t.Fatalf("старый пароль: %v", err)
	}
	if _, err := e.app.Login(context.Background(), m.Email, newPassword, metaWeb); err != nil {
		t.Fatalf("новый пароль: %v", err)
	}
	err := e.app.ChangePassword(context.Background(), s, newPassword, "", m.Codes[3], testPassword+" 2", metaWeb)
	wantFactorFailure(t, err, staff.ErrMFAInvalid, app.MaxFactorFailures-1)
}

// Неверный текущий пароль — staff.password_invalid, неверный код — staff.mfa_invalid; оба — в
// счётчик второго фактора (staff.factor_failed, шаг password_change). При неверном пароле второй
// фактор не проверяется: резервный код не сгорает. Успех обнуляет счётчик. Пятая подряд закрывает
// учётку и гасит сессии.
func TestChangePasswordFailures(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{Role: "support"})
	s := e.load(e.signIn(m))
	ctx := context.Background()

	err := e.app.ChangePassword(ctx, s, "не тот пароль", "", m.Codes[0], newPassword, metaWeb)
	wantFactorFailure(t, err, staff.ErrPasswordInvalid, 4)
	if n := e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NOT NULL`, m.ID); n != 0 {
		t.Fatal("резервный код сгорел при неверном пароле")
	}
	err = e.app.ChangePassword(ctx, s, testPassword, "000000", "", newPassword, metaWeb)
	wantFactorFailure(t, err, staff.ErrMFAInvalid, 3)
	err = e.app.ChangePassword(ctx, s, strings.Repeat("я", 2000), "000000", "", newPassword, metaWeb)
	wantFactorFailure(t, err, staff.ErrPasswordInvalid, 2) // слишком длинный текущий — просто неверный
	rows := e.audits("staff.factor_failed")
	if len(rows) != 3 || rows[0].Role != "support" || !strings.Contains(rows[0].After, `"step": "password_change"`) {
		t.Fatalf("аудит неудач %+v", rows)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 3 {
		t.Fatalf("счётчик %d, ждали 3", n)
	}

	// успех (TOTP следующего шага) обнуляет счётчик
	e.clock.Advance(30 * time.Second)
	if err := e.app.ChangePassword(ctx, s, testPassword, e.totp(m), "", newPassword, metaWeb); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик после успеха %d", n)
	}
	if rows := e.audits("staff.password_changed"); len(rows) != 1 || !strings.Contains(rows[0].After, `"method": "totp"`) {
		t.Fatalf("аудит успеха %+v", rows)
	}

	// пять неудач подряд — блокировка, сессии погашены
	for want := 4; want >= 0; want-- {
		err := e.app.ChangePassword(ctx, s, "не тот пароль", "000000", "", testPassword, metaWeb)
		wantFactorFailure(t, err, staff.ErrPasswordInvalid, want)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND locked_until = $2`, m.ID, e.clock.Now().Add(time.Hour)); n != 1 {
		t.Fatal("учётка не закрыта")
	}
	if r := e.revokedReason(s.SessionID); r != "locked" {
		t.Fatalf("текущая сессия после блокировки: %q", r)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'staff.locked' AND aggregate_id = $1`, m.ID); n != 1 {
		t.Fatalf("событий staff.locked %d", n)
	}
	// дальше — отказ сессии без записи неудачи
	err = e.app.ChangePassword(ctx, s, "не тот пароль", "000000", "", testPassword, metaWeb)
	if !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("после блокировки: %v", err)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 5 {
		t.Fatalf("счётчик %d — отказ сессии записан как неудача", n)
	}
}

// Погашенная сессия (вышли в другой вкладке) — auth.ErrSessionInvalid без записи неудачи и без
// проверки пароля.
func TestChangePasswordRevokedSession(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	if err := e.app.Logout(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	err := e.app.ChangePassword(context.Background(), s, "не тот пароль", "000000", "", newPassword, metaWeb)
	if !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("погашенная сессия: %v", err)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик %d", n)
	}
}

// Параллельные неверные текущие пароли не обходят порог «5 подряд»: пять неудач (attempts_left
// 4…0), остальные — отказ погашенной блокировкой сессии; одна блокировка.
func TestChangePasswordConcurrentFailures(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	const tries = 8
	var (
		wg   sync.WaitGroup
		errs [tries]error
	)
	for i := range tries {
		wg.Go(func() {
			errs[i] = e.app.ChangePassword(context.Background(), s, "не тот пароль", "", m.Codes[0], newPassword, metaWeb)
		})
	}
	wg.Wait()
	var left []int
	invalid := 0
	for i, err := range errs {
		if me, ok := errors.AsType[*staff.MFAError](err); ok && errors.Is(err, staff.ErrPasswordInvalid) {
			left = append(left, me.AttemptsLeft)
			continue
		}
		if !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatalf("попытка %d: %v", i, err)
		}
		invalid++
	}
	slices.Sort(left)
	if !slices.Equal(left, []int{0, 1, 2, 3, 4}) || invalid != tries-5 {
		t.Fatalf("attempts_left %v, отказов сессии %d", left, invalid)
	}
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 5 {
		t.Fatalf("счётчик %d", n)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'staff.locked'`); n != 1 {
		t.Fatalf("событий staff.locked %d", n)
	}
}

// Политика нового пароля и выбор второго фактора — ошибки поля до транзакции: неудача не
// пишется, пароль не меняется.
func TestChangePasswordValidation(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	ctx := context.Background()
	for next, code := range map[string]string{
		"короткий":                "too_short",
		"1234567890":              "common",
		strings.Repeat("ю", 129):  "too_long",
		strings.Repeat("я", 2000): "too_long",
	} {
		err := e.app.ChangePassword(ctx, s, testPassword, "", m.Codes[0], next, metaWeb)
		wantFieldError(t, err, "body.new_password", code)
	}
	err := e.app.ChangePassword(ctx, s, testPassword, "", "", newPassword, metaWeb)
	wantFieldError(t, err, "body.code", "required")
	err = e.app.ChangePassword(ctx, s, testPassword, "123456", m.Codes[0], newPassword, metaWeb)
	wantFieldError(t, err, "body.code", "oneOf")
	if n := e.count(`SELECT mfa_failures FROM staff_accounts WHERE id = $1`, m.ID); n != 0 {
		t.Fatalf("счётчик %d", n)
	}
	if n := e.count(`SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1 AND used_at IS NOT NULL`, m.ID); n != 0 {
		t.Fatal("резервный код сгорел на ошибке поля")
	}
	if _, err := e.app.Login(ctx, m.Email, testPassword, metaWeb); err != nil {
		t.Fatalf("пароль изменился: %v", err)
	}
}

// Язык: ru|en сохраняется с аудитом (было/стало); тот же язык — без записи; иное — ошибка поля.
func TestSetLocale(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	s := e.load(e.signIn(m))
	ctx := context.Background()
	if err := e.app.SetLocale(ctx, s, "en", metaWeb); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND locale = 'en'`, m.ID); n != 1 {
		t.Fatal("язык не сохранён")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.locale_changed' AND actor_staff_id = $1
		AND before->>'locale' = 'ru' AND after->>'locale' = 'en'`, m.ID); n != 1 {
		t.Fatal("аудит смены языка")
	}
	e.wantAuditMeta("staff.locale_changed")
	if err := e.app.SetLocale(ctx, s, "en", metaWeb); err != nil {
		t.Fatal(err)
	}
	if n := len(e.audits("staff.locale_changed")); n != 1 {
		t.Fatalf("записей %d — тот же язык записан", n)
	}
	for _, bad := range []string{"de", "", "EN", "en-US"} {
		wantFieldError(t, e.app.SetLocale(ctx, s, bad, metaWeb), "body.locale", "enum")
	}
	if err := e.app.SetLocale(ctx, s, "ru", metaWeb); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND locale = 'ru'`, m.ID); n != 1 {
		t.Fatal("язык не вернулся")
	}
}
