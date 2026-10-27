package app_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

// Потолок журнала неудач принятия — не больше 20 записей за час на почту (документ: спека §4.4,
// контракт staffAcceptInvite): всплеск 20 в одну минуту, затем неудачи каждые 3 минуты весь остаток
// часа — записей так и остаётся 20; новая — только через час после всплеска.
func TestAcceptInviteFailureAuditHourly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	fail := func() {
		t.Helper()
		if _, err := e.app.AcceptInvite(ctx, m.Email, otherCode(code), newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
			t.Fatal(err)
		}
	}
	for range 20 {
		fail()
	}
	for range 19 { // t0+3 мин … t0+57 мин
		e.clock.Advance(3 * time.Minute)
		fail()
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != inviteAuditCap {
		t.Fatalf("за час записей %d, ждали %d", n, inviteAuditCap)
	}
	e.clock.Advance(3 * time.Minute) // ровно час после всплеска
	fail()
	if n := len(e.audits("staff.invite_accept_failed")); n != inviteAuditCap+1 {
		t.Fatalf("через час записей %d", n)
	}
}

// failingLimiter — хранилище лимитов недоступно: любой вызов — ошибка.
type failingLimiter struct{}

var errLimiterDown = errors.New("лимиты недоступны")

func (failingLimiter) Allow(context.Context, string, ratelimit.Policy) (ratelimit.Result, error) {
	return ratelimit.Result{}, errLimiterDown
}
func (failingLimiter) Add(context.Context, string, ratelimit.Policy) error { return errLimiterDown }
func (failingLimiter) Over(context.Context, string, ratelimit.Policy) (bool, error) {
	return false, errLimiterDown
}

// limiterDownApp — сценарии с недоступным хранилищем лимитов.
func (e *env) limiterDownApp() *app.App {
	d := e.deps
	d.Limiter = failingLimiter{}
	return app.New(d)
}

// Хранилище лимитов недоступно: неверный код — тот же staff.invite_invalid, а запись неудачи идёт
// (лучше лишняя, чем потерянная); верный код принимается. Вход в том же положении — неверный пароль
// отвечает staff.invalid_credentials с записью, верный проходит (лимит по почте пропускается, как
// в ratelimit.Middleware).
func TestLimiterDownStillAudits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.limiterDownApp()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	if _, err := a.AcceptInvite(ctx, m.Email, otherCode(code), newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("принятие: %v", err)
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != 1 {
		t.Fatalf("записей неудачи принятия %d", n)
	}
	if _, err := a.AcceptInvite(ctx, m.Email, code, newPassword, "", metaWeb); err != nil {
		t.Fatalf("верный код: %v", err)
	}

	member := e.addMember("olga.v@wf.example", memberOpts{})
	if _, err := a.Login(ctx, member.Email, "не тот пароль 12345", metaWeb); !errors.Is(err, staff.ErrInvalidCredentials) {
		t.Fatalf("вход с неверным паролем: %v", err)
	}
	if n := len(e.audits("staff.login_failed")); n != 1 {
		t.Fatalf("записей неудачи входа %d", n)
	}
	if _, err := a.Login(ctx, member.Email, testPassword, metaWeb); err != nil {
		t.Fatalf("вход: %v", err)
	}
}

var mailCode = regexp.MustCompile(`[a-z2-7]{5}-[a-z2-7]{5}`)

// Самоотправка лечит нечитаемый код (ключ убран раньше rekey): в той же строке — новый код текущим
// ключом, письмо с ним, принятие им проходит; срок не продлевается, отправка считается. Аноним
// сделать код нечитаемым не может — это не срыв, а самовосстановление после ошибки оператора.
// Читаемый код самоотправка по-прежнему не меняет (TestResendInviteSelfSameCode).
func TestResendInviteSelfHealsUnreadableCode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, oldCode := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	before := e.liveInvite(m.ID)
	onlyNew, _ := newRing(t)
	a, logs := e.logApp(onlyNew)
	e.clock.Advance(61 * time.Second)
	if err := a.ResendInviteSelf(ctx, m.Email, metaWeb); err != nil {
		t.Fatal(err)
	}
	after := e.liveInvite(m.ID)
	if after.ID != before.ID || after.Sends != 2 || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("строка %+v → %+v", before, after)
	}
	if n := e.count(`SELECT count(*) FROM staff_invites WHERE id = $1 AND code_key_id = $2`, after.ID, onlyNew.CurrentID()); n != 1 {
		t.Fatal("код не перешифрован текущим ключом")
	}
	msg, err := a.ComposeInvite(ctx, after.ID, m.ID, staff.MailInvite)
	if err != nil {
		t.Fatalf("письмо: %v", err)
	}
	newCode := mailCode.FindString(msg.Text)
	if newCode == "" || newCode == oldCode {
		t.Fatalf("код в письме %q (старый %q)", newCode, oldCode)
	}
	if !regexp.MustCompile(`level=WARN.*` + after.ID.String()).MatchString(logs.String()) {
		t.Fatalf("нет предупреждения о перевыпуске:\n%s", logs.String())
	}
	// новый код — секрет: в журнал сервера не попадает ни в каком виде
	if s := logs.String(); strings.Contains(s, newCode) || strings.Contains(s, strings.ReplaceAll(newCode, "-", "")) {
		t.Fatalf("код в журнале сервера:\n%s", s)
	}
	if _, err := a.AcceptInvite(ctx, m.Email, newCode, newPassword, "", metaWeb); err != nil {
		t.Fatalf("принятие новым кодом: %v", err)
	}
}
