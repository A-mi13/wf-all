package app_test

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

const (
	purposeInvite = "invite"
	purposeReset  = "access_reset"
)

// issue — новое приглашение учётки staffID (IssueInvite в транзакции вызывающего); код — как в письме.
func (e *env) issue(staffID uuid.UUID, purpose string) string {
	e.t.Helper()
	var code string
	err := db.InTx(context.Background(), e.owner, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		code, err = e.app.IssueInvite(ctx, tx, staffID, purpose)
		return err
	})
	if err != nil {
		e.t.Fatalf("IssueInvite: %v", err)
	}
	return code
}

// invitee — учётка invited без второго фактора и с живым приглашением purpose.
func (e *env) invitee(email string, o memberOpts, purpose string) (member, string) {
	e.t.Helper()
	o.Status, o.NoMFA = "invited", true
	m := e.addMember(email, o)
	return m, e.issue(m.ID, purpose)
}

// inviteRow — строка staff_invites.
type inviteRow struct {
	ID                    uuid.UUID
	Purpose               string
	HasCode, Live         bool
	Attempts, Sends       int
	ExpiresAt, LastSentAt time.Time
	CreatedAt             time.Time
}

// invites — все приглашения учётки, новые последними.
func (e *env) invites(staffID uuid.UUID) []inviteRow {
	e.t.Helper()
	rows, err := e.owner.Query(context.Background(), `SELECT id, purpose::text, code_enc IS NOT NULL,
		consumed_at IS NULL AND revoked_at IS NULL, code_attempts, sends, expires_at, last_sent_at, created_at
		FROM staff_invites WHERE staff_id = $1 ORDER BY created_at, id`, staffID)
	if err != nil {
		e.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (inviteRow, error) {
		var i inviteRow
		err := r.Scan(&i.ID, &i.Purpose, &i.HasCode, &i.Live, &i.Attempts, &i.Sends, &i.ExpiresAt, &i.LastSentAt, &i.CreatedAt)
		return i, err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// liveInvite — единственное живое приглашение учётки.
func (e *env) liveInvite(staffID uuid.UUID) inviteRow {
	e.t.Helper()
	var live []inviteRow
	for _, i := range e.invites(staffID) {
		if i.Live {
			live = append(live, i)
		}
	}
	if len(live) != 1 {
		e.t.Fatalf("живых приглашений %d", len(live))
	}
	return live[0]
}

// mails — задачи staff.mail вида kind (ref и recipient — uuid.Nil: любые).
func (e *env) mails(kind string, ref, recipient uuid.UUID) int {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail' AND queue = 'staff'
		AND args->>'kind' = $1 AND ($2::uuid IS NULL OR (args->>'ref')::uuid = $2)
		AND ($3::uuid IS NULL OR (args->>'recipient')::uuid = $3)`, kind, nilUUID(ref), nilUUID(recipient))
}

func nilUUID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}

// currentCode — код живого приглашения учётки, расшифрованный кольцом теста (код повторной
// отправки сценарий не возвращает).
func (e *env) currentCode(staffID uuid.UUID) string {
	e.t.Helper()
	var (
		inviteID uuid.UUID
		ct       []byte
		keyID    string
	)
	if err := e.owner.QueryRow(context.Background(), `SELECT id, code_enc, code_key_id FROM staff_invites
		WHERE staff_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, staffID).Scan(&inviteID, &ct, &keyID); err != nil {
		e.t.Fatal(err)
	}
	code, err := e.ring.Open(ct, keyID, inviteID[:])
	if err != nil {
		e.t.Fatal(err)
	}
	return string(code)
}

// accept — принятие приглашения m кодом code с новым паролем; токен и сессия setup.
func (e *env) accept(m member, code string) (string, *auth.Staff) {
	e.t.Helper()
	tok, err := e.app.AcceptInvite(context.Background(), m.Email, code, newPassword, "", metaWeb)
	if err != nil {
		e.t.Fatalf("AcceptInvite: %v", err)
	}
	return tok, e.load(tok)
}

// passwordIs — хэш пароля учётки подходит к pw.
func (e *env) passwordIs(staffID uuid.UUID, pw string) bool {
	e.t.Helper()
	var hash string
	if err := e.owner.QueryRow(context.Background(), `SELECT password_hash FROM staff_accounts WHERE id = $1`, staffID).Scan(&hash); err != nil {
		e.t.Fatal(err)
	}
	h, err := sharedHasher()
	if err != nil {
		e.t.Fatal(err)
	}
	ok, _, err := h.Verify(context.Background(), pw, hash)
	if err != nil {
		e.t.Fatal(err)
	}
	return ok
}

// IssueInvite (§4.3): живое приглашение на 24 ч с одной отправкой и шифрованным кодом (6 цифр);
// прежнее живое гасится (код стирается); письмо — задача staff.mail вида по назначению с ref =
// id приглашения и recipient = учётка в той же транзакции.
func TestIssueInvite(t *testing.T) {
	e := newEnv(t)
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		t.Fatalf("код %q", code)
	}
	first := e.liveInvite(m.ID)
	if first.Purpose != purposeInvite || !first.HasCode || first.Sends != 1 || first.Attempts != 0 ||
		!first.ExpiresAt.Equal(t0.Add(24*time.Hour)) || !first.LastSentAt.Equal(t0) || !first.CreatedAt.Equal(t0) {
		t.Fatalf("приглашение %+v", first)
	}
	if n := e.mails(staff.MailInvite, first.ID, m.ID); n != 1 {
		t.Fatalf("писем staff.invite %d", n)
	}
	if n := e.count(`SELECT count(*) FROM staff_invites WHERE id = $1 AND position(convert_to($2, 'UTF8') in code_enc) > 0`,
		first.ID, code); n != 0 {
		t.Fatal("код в базе открытым текстом")
	}

	e.clock.Advance(time.Hour)
	again := e.issue(m.ID, purposeReset)
	all := e.invites(m.ID)
	if len(all) != 2 || all[0].Live || all[0].HasCode {
		t.Fatalf("прежнее не погашено: %+v", all)
	}
	if all[1].Purpose != purposeReset || !all[1].Live || !all[1].ExpiresAt.Equal(t0.Add(25*time.Hour)) {
		t.Fatalf("новое %+v", all[1])
	}
	if n := e.mails(staff.MailAccessReset, all[1].ID, m.ID); n != 1 {
		t.Fatalf("писем staff.access_reset %d", n)
	}
	// старый код больше не подходит, новый — да
	if _, err := e.app.AcceptInvite(context.Background(), m.Email, code, newPassword, "", metaWeb); code != again &&
		!errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("старый код: %v", err)
	}
	e.accept(m, again)
}

// Принятие (§4.4): пароль сохранён, неподтверждённый TOTP и коды сброшены, все сессии учётки и
// сессия старой cookie (чья угодно) погашены replaced, новая сессия — setup на 8 ч; приглашение
// живо; аудит staff.invite_accepted от учётки. Повтор тем же кодом — снова успех.
func TestAcceptInvite(t *testing.T) {
	e := newEnv(t)
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support", Name: "Иван П."}, purposeInvite)
	// следы прежнего доступа: неподтверждённый секрет, резервный код, сессии
	e.exec(`INSERT INTO staff_mfa (staff_id, secret_enc, key_id) VALUES ($1, '\x00', 'AAAAAAAAAAA')`, m.ID)
	e.exec(`INSERT INTO staff_recovery_codes (staff_id, code_mac, key_id) VALUES ($1, decode(repeat('ab', 32), 'hex'), 'AAAAAAAAAAA')`, m.ID)
	_, ownSetup := e.rawSession(m.ID, auth.StageSetup, ipHome, "Safari", t0.Add(-time.Hour))
	other := e.addMember("other@wf.example", memberOpts{})
	otherTok := e.signIn(other)
	otherSession := e.load(otherTok).SessionID

	tok, err := e.app.AcceptInvite(context.Background(), "  Ivan.P@WF.example ", code, newPassword, otherTok, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	s := e.load(tok)
	if s.Stage != auth.StageSetup || s.StaffID != m.ID || s.Reauth {
		t.Fatalf("сессия %+v", s)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1 AND created_at = $2 AND expires_at = $3
		AND mfa_at IS NULL AND ip = $4 AND user_agent = $5`, s.SessionID, t0, t0.Add(8*time.Hour), ipHome, metaWeb.UserAgent); n != 1 {
		t.Fatal("сроки или адрес новой сессии")
	}
	if r := e.revokedReason(ownSetup); r != "replaced" {
		t.Fatalf("своя прежняя сессия: %q", r)
	}
	if r := e.revokedReason(otherSession); r != "replaced" {
		t.Fatalf("сессия старой cookie: %q", r)
	}
	if !e.passwordIs(m.ID, newPassword) {
		t.Fatal("пароль не сохранён")
	}
	if n := e.count(`SELECT (SELECT count(*) FROM staff_mfa WHERE staff_id = $1) + (SELECT count(*) FROM staff_recovery_codes WHERE staff_id = $1)`, m.ID); n != 0 {
		t.Fatalf("второй фактор не сброшен: %d", n)
	}
	inv := e.liveInvite(m.ID)
	if !inv.HasCode || inv.Attempts != 0 {
		t.Fatalf("приглашение %+v", inv)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND status = 'invited'`, m.ID); n != 1 {
		t.Fatal("статус учётки сменился до ack")
	}
	rows := e.audits("staff.invite_accepted")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != m.ID || rows[0].Role != "support" ||
		rows[0].Label == nil || *rows[0].Label != "Иван П." {
		t.Fatalf("аудит %+v", rows)
	}
	e.wantAuditMeta("staff.invite_accepted")

	// потерянная сессия настройки: повтор тем же кодом — новая сессия, прежняя погашена
	e.clock.Advance(time.Minute)
	tok2, err := e.app.AcceptInvite(context.Background(), m.Email, code, newPassword, "", metaWeb)
	if err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if e.load(tok2).SessionID == s.SessionID || e.revokedReason(s.SessionID) != "replaced" {
		t.Fatal("повтор не заменил сессию")
	}
}

// 5 неверных кодов стирают код (D12), приглашение живо; шестая попытка — даже верным кодом — тот же
// staff.invite_invalid без нарушения CHECK (23514). Каждая неудача закоммичена (D8) и в аудите
// без автора с маскированной почтой.
func TestAcceptInviteWrongCodeErases(t *testing.T) {
	e := newEnv(t)
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 1; i <= 5; i++ {
		_, err := e.app.AcceptInvite(context.Background(), m.Email, wrong, newPassword, "", metaWeb)
		if !errors.Is(err, staff.ErrInviteInvalid) {
			t.Fatalf("попытка %d: %v", i, err)
		}
		inv := e.liveInvite(m.ID)
		if inv.Attempts != i || inv.HasCode != (i < 5) {
			t.Fatalf("после %d неудач: %+v", i, inv)
		}
	}
	_, err := e.app.AcceptInvite(context.Background(), m.Email, code, newPassword, "", metaWeb)
	if !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("верный код после стирания: %v", err)
	}
	if inv := e.liveInvite(m.ID); inv.Attempts != 5 || inv.HasCode {
		t.Fatalf("после стирания %+v", inv)
	}
	rows := e.audits("staff.invite_accept_failed")
	if len(rows) != 5 {
		t.Fatalf("аудит неудач %d", len(rows))
	}
	for _, r := range rows {
		if r.Role != "anonymous" || r.StaffID != nil || r.ObjectID == nil || *r.ObjectID != m.ID ||
			strings.Contains(r.After, m.Email) || !strings.Contains(r.After, "attempts_left") {
			t.Fatalf("запись %+v", r)
		}
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE staff_id = $1`, m.ID); n != 0 {
		t.Fatalf("сессий %d", n)
	}
	// новый код повторной отправкой снова работает (попытки с нуля)
	e.clock.Advance(time.Minute)
	if err := e.app.ResendInviteSelf(context.Background(), m.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	if inv := e.liveInvite(m.ID); inv.Attempts != 0 || !inv.HasCode || inv.Sends != 2 {
		t.Fatalf("после повторной отправки %+v", inv)
	}
}

// Политика пароля — ошибка поля body.password до обращения к базе: сценарий без пула не падает,
// попытка кода не тратится.
func TestAcceptInvitePasswordPolicy(t *testing.T) {
	e := newEnv(t)
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	noDB := e.deps
	noDB.Pool = nil // любое обращение к базе — паника
	a := app.New(noDB)
	for pw, rule := range map[string]string{
		"короткий":                "too_short",
		"1234567890":              "common",
		strings.Repeat("я", 129):  "too_long",
		strings.Repeat("ю", 2000): "too_long",
	} {
		_, err := a.AcceptInvite(context.Background(), m.Email, "000000", pw, "", metaWeb)
		wantFieldError(t, err, "body.password", rule)
	}
	if _, err := e.app.AcceptInvite(context.Background(), m.Email, code, "короткий", "", metaWeb); err == nil {
		t.Fatal("короткий пароль принят")
	}
	if inv := e.liveInvite(m.ID); inv.Attempts != 0 {
		t.Fatalf("попытка потрачена: %+v", inv)
	}
}

// Истёкшее, погашенное, чужая почта, учётка не invited, нет почты, кривая почта — один и тот же
// staff.invite_invalid.
func TestAcceptInviteInvalid(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	n, _ := e.invitee("nina.k@wf.example", memberOpts{Role: "support"}, purposeInvite)
	active := e.addMember("active@wf.example", memberOpts{})
	activeCode := e.issue(active.ID, purposeReset)
	off, offCode := e.invitee("off@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.exec(`UPDATE staff_accounts SET status = 'disabled' WHERE id = $1`, off.ID)
	revoked, revokedCode := e.invitee("rev@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.exec(`UPDATE staff_invites SET revoked_at = $2, code_enc = NULL, code_key_id = NULL WHERE staff_id = $1`, revoked.ID, t0)

	cases := []struct{ name, email, code string }{
		{"чужая почта", n.Email, code},
		{"учётка active", active.Email, activeCode},
		{"учётка выключена", off.Email, offCode},
		{"погашено", revoked.Email, revokedCode},
		{"нет почты", "nobody@wf.example", code},
		{"кривая почта", "не почта", code},
		{"кривой код", m.Email, "12a456"},
	}
	for _, c := range cases {
		if _, err := e.app.AcceptInvite(ctx, c.email, c.code, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	e.clock.Advance(24 * time.Hour)
	if _, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("истёкшее: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions`); n != 0 {
		t.Fatalf("сессий %d", n)
	}
}

// Старая блокировка второго фактора (locked_until, счётчик) после принятия не закрывает ни
// сессию setup, ни вход после настройки.
func TestAcceptInviteClearsStaleLock(t *testing.T) {
	e := newEnv(t)
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.exec(`UPDATE staff_accounts SET locked_until = $2, mfa_failures = 5 WHERE id = $1`, m.ID, t0.Add(30*time.Minute))
	_, s := e.accept(m, code)
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND locked_until IS NULL AND mfa_failures = 0`, m.ID); n != 1 {
		t.Fatal("блокировка осталась")
	}
	e.completeSetup(s)
}

// Повторная отправка самим (§4.3 п.3): пауза 60 с, не больше 5 отправок за жизнь приглашения
// (сутки), срок не продлевается, новый код в той же строке; несуществующая и кривая почта — nil
// без письма. Аудит отправки — без автора, почта маскирована.
func TestResendInviteSelf(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	inv := e.liveInvite(m.ID)

	if err := e.app.ResendInviteSelf(ctx, m.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	if got := e.liveInvite(m.ID); got.Sends != 1 || e.mails(staff.MailInvite, inv.ID, m.ID) != 1 {
		t.Fatalf("раньше паузы: %+v", got)
	}
	for i := 2; i <= 5; i++ {
		e.clock.Advance(61 * time.Second)
		if err := e.app.ResendInviteSelf(ctx, strings.ToUpper(m.Email), metaWeb); err != nil {
			t.Fatal(err)
		}
		got := e.liveInvite(m.ID)
		if got.ID != inv.ID || got.Sends != i || !got.ExpiresAt.Equal(inv.ExpiresAt) || !got.LastSentAt.Equal(e.clock.Now()) {
			t.Fatalf("отправка %d: %+v", i, got)
		}
	}
	if n := e.mails(staff.MailInvite, inv.ID, m.ID); n != 5 {
		t.Fatalf("писем %d", n)
	}
	// старый код заменён
	if _, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("старый код: %v", err)
	}
	// шестая за сутки — без письма
	e.clock.Advance(time.Hour)
	if err := e.app.ResendInviteSelf(ctx, m.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	if got := e.liveInvite(m.ID); got.Sends != 5 || e.mails(staff.MailInvite, uuid.Nil, uuid.Nil) != 5 {
		t.Fatalf("сверх лимита: %+v", got)
	}
	for _, email := range []string{"nobody@wf.example", "не почта", ""} {
		if err := e.app.ResendInviteSelf(ctx, email, metaWeb); err != nil {
			t.Fatalf("%q: %v", email, err)
		}
	}
	if n := e.mails(staff.MailInvite, uuid.Nil, uuid.Nil); n != 5 {
		t.Fatalf("письма без приглашения: %d", n)
	}
	rows := e.audits("staff.invite_resent")
	if len(rows) != 4 {
		t.Fatalf("аудит %d", len(rows))
	}
	for _, r := range rows {
		if r.Role != "anonymous" || r.StaffID != nil || r.ObjectID == nil || *r.ObjectID != m.ID || strings.Contains(r.After, m.Email) {
			t.Fatalf("запись %+v", r)
		}
	}

	// истёкшее и учётка не invited — без письма
	n, _ := e.invitee("nina.k@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.clock.Advance(24 * time.Hour)
	if err := e.app.ResendInviteSelf(ctx, n.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	active := e.addMember("active@wf.example", memberOpts{})
	e.issue(active.ID, purposeReset)
	e.clock.Advance(time.Minute)
	if err := e.app.ResendInviteSelf(ctx, active.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	if got := e.liveInvite(n.ID); got.Sends != 1 {
		t.Fatalf("истёкшее: %+v", got)
	}
	if got := e.liveInvite(active.ID); got.Sends != 1 {
		t.Fatalf("учётка active: %+v", got)
	}
}

// Две быстрые повторные отправки одновременно — одно письмо (строка под замком).
func TestResendInviteSelfRace(t *testing.T) {
	e := newEnv(t)
	m, _ := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.clock.Advance(61 * time.Second)
	var (
		wg   sync.WaitGroup
		errs [4]error
	)
	for i := range errs {
		wg.Go(func() { errs[i] = e.app.ResendInviteSelf(context.Background(), m.Email, metaWeb) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := e.liveInvite(m.ID); got.Sends != 2 {
		t.Fatalf("отправок %d", got.Sends)
	}
	if n := e.mails(staff.MailInvite, uuid.Nil, m.ID); n != 2 {
		t.Fatalf("писем %d", n)
	}
}

// Повторная отправка админом (§4.3 п.4): право staff.invites.resend, не над собой, только invited,
// пауза 60 с — ResendTooSoonError с остатком; новое приглашение того же назначения на 24 ч заново;
// аудит от сотрудника.
func TestResendInvite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.addMember("owner@wf.example", memberOpts{Role: "owner", Name: "Анна В."})
	s := e.load(e.signIn(owner))
	m, _ := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeReset)
	first := e.liveInvite(m.ID)

	err := e.app.ResendInvite(ctx, s, m.ID, metaWeb)
	if tooSoon, ok := errors.AsType[*staff.ResendTooSoonError](err); !ok || tooSoon.RetryAfter != time.Minute {
		t.Fatalf("сразу: %v", err)
	}
	e.clock.Advance(20 * time.Second)
	err = e.app.ResendInvite(ctx, s, m.ID, metaWeb)
	if tooSoon, ok := errors.AsType[*staff.ResendTooSoonError](err); !ok || tooSoon.RetryAfter != 40*time.Second {
		t.Fatalf("через 20 с: %v", err)
	}
	e.clock.Advance(40 * time.Second)
	if err := e.app.ResendInvite(ctx, s, m.ID, metaWeb); err != nil {
		t.Fatal(err)
	}
	all := e.invites(m.ID)
	if len(all) != 2 || all[0].ID != first.ID || all[0].Live || all[0].HasCode {
		t.Fatalf("прежнее не погашено: %+v", all)
	}
	now := e.clock.Now()
	if got := all[1]; !got.Live || got.Purpose != purposeReset || got.Sends != 1 || !got.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("новое %+v", got)
	}
	if n := e.mails(staff.MailAccessReset, all[1].ID, m.ID); n != 1 {
		t.Fatalf("писем %d", n)
	}
	rows := e.audits("staff.invite_resent")
	if len(rows) != 1 || rows[0].StaffID == nil || *rows[0].StaffID != owner.ID || rows[0].Role != "owner" ||
		rows[0].Label == nil || *rows[0].Label != "Анна В." || rows[0].ObjectID == nil || *rows[0].ObjectID != m.ID {
		t.Fatalf("аудит %+v", rows)
	}
	e.wantAuditMeta("staff.invite_resent")

	// ошибки сценария
	active := e.addMember("active@wf.example", memberOpts{Role: "support"})
	if err := e.app.ResendInvite(ctx, s, owner.ID, metaWeb); !errors.Is(err, staff.ErrSelfAction) {
		t.Fatalf("себе: %v", err)
	}
	if err := e.app.ResendInvite(ctx, s, uuid.New(), metaWeb); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет учётки: %v", err)
	}
	if err := e.app.ResendInvite(ctx, s, active.ID, metaWeb); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("учётка active: %v", err)
	}
	mod := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{e.city("kazan")}})
	err = e.app.ResendInvite(ctx, e.load(e.signIn(mod)), m.ID, metaWeb)
	if pe, ok := errors.AsType[httpx.ProblemError](err); !ok || pe.Problem().Status != http.StatusForbidden ||
		pe.Problem().Code != httpx.CodeForbidden {
		t.Fatalf("модератор: %v", err)
	}
	// без приглашений (не бывает, но назначение по умолчанию — invite) — новое приглашение
	bare := e.addMember("bare@wf.example", memberOpts{Role: "support", Status: "invited", NoMFA: true})
	if err := e.app.ResendInvite(ctx, s, bare.ID, metaWeb); err != nil {
		t.Fatal(err)
	}
	if got := e.liveInvite(bare.ID); got.Purpose != purposeInvite {
		t.Fatalf("назначение %+v", got)
	}
}

// Секреты (код приглашения, секрет TOTP, резервные коды) не попадают ни в журнал, ни в аргументы
// задач River.
func TestNoSecretsInAuditAndJobs(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	code, err := e.app.OperatorBootstrap(ctx, "new.owner@wf.example", "Новый В.")
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{code}
	nm := member{Email: "new.owner@wf.example"}
	if err := e.owner.QueryRow(ctx, `SELECT id FROM staff_accounts WHERE email = $1`, nm.Email).Scan(&nm.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.AcceptInvite(ctx, nm.Email, "999999", newPassword, "", metaWeb); err == nil && code != "999999" {
		t.Fatal("неверный код принят")
	}
	_, s := e.accept(nm, code)
	secret, _, err := e.app.MFASetup(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, secret)
	nm.Secret = decodeSecret(t, secret)
	codes, err := e.app.MFAConfirm(ctx, s, e.totp(nm), metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, codes...)
	tok, err := e.app.RecoveryAck(ctx, s, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(30 * time.Second)
	fresh, err := e.app.RegenerateRecovery(ctx, e.load(tok), e.totp(nm), metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, fresh...)
	m, inviteCode := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	secrets = append(secrets, inviteCode)
	e.clock.Advance(time.Minute)
	if err := e.app.ResendInvite(ctx, e.load(e.signIn(owner)), m.ID, metaWeb); err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, e.currentCode(m.ID))
	e.clock.Advance(time.Minute)
	if err := e.app.ResendInviteSelf(ctx, m.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, e.currentCode(m.ID))
	resent, err := e.app.OperatorInviteResend(ctx, m.Email)
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, resent)

	var texts []string
	rows, err := e.owner.Query(ctx, `SELECT coalesce(before::text, '') || coalesce(after::text, '') || coalesce(reason, '') FROM audit_log
		UNION ALL SELECT args::text FROM river_job`)
	if err != nil {
		t.Fatal(err)
	}
	texts, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) < 10 {
		t.Fatalf("записей %d — проверка вхолостую", len(texts))
	}
	for _, sec := range secrets {
		// секрет — отдельным словом: 6 цифр кода могут случайно встретиться внутри uuid
		norm := strings.ReplaceAll(sec, "-", "")
		re := regexp.MustCompile(`(?i)(^|[^0-9a-z])(` + regexp.QuoteMeta(sec) + `|` + regexp.QuoteMeta(norm) + `)([^0-9a-z]|$)`)
		for _, txt := range texts {
			if re.MatchString(txt) {
				t.Errorf("секрет %q в %s", sec, txt)
			}
		}
	}
}
