package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

// inviteAuditCap — потолок записей staff.invite_accept_failed на почту в час (inviteAuditPolicy).
const inviteAuditCap = 20

// Любой исход «принять нечего» — одна анонимная запись staff.invite_accept_failed (почта
// маскирована), как у входа: нет почты, кривая почта, учётка не invited, приглашение погашено или
// истекло, неверный код. Пути «нет приглашения» и «неверный код» пишут одинаково — по журналу
// существование почты не отличить.
func TestAcceptInviteFailureAuditedAlways(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	active := e.addMember("active@wf.example", memberOpts{})
	expired, expiredCode := e.invitee("expired@wf.example", memberOpts{Role: "support"}, purposeInvite)
	e.exec(`UPDATE staff_invites SET expires_at = $2 WHERE staff_id = $1`, expired.ID, t0)
	cases := []struct{ name, email, code string }{
		{"нет почты", "nobody@wf.example", code},
		{"кривая почта", "не почта", code},
		{"учётка active", active.Email, code},
		{"истекло", expired.Email, expiredCode},
		{"неверный код", m.Email, otherCode(code)},
	}
	for i, c := range cases {
		if _, err := e.app.AcceptInvite(ctx, c.email, c.code, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
			t.Fatalf("%s: %v", c.name, err)
		}
		rows := e.audits("staff.invite_accept_failed")
		if len(rows) != i+1 {
			t.Fatalf("%s: записей %d, ждали %d", c.name, len(rows), i+1)
		}
		r := rows[i]
		if r.Role != "anonymous" || r.StaffID != nil || !strings.Contains(r.After, `"email"`) ||
			strings.Contains(r.After, c.email) || strings.Contains(r.After, code) {
			t.Fatalf("%s: запись %+v", c.name, r)
		}
	}
	if r := e.audits("staff.invite_accept_failed")[0]; r.ObjectID != nil {
		t.Fatalf("нет почты — объект %v", *r.ObjectID)
	}
}

// Потолок журнала: 100 неверных кодов одной почты — записей не больше потолка за час (журнал не
// засыпать), ответ тот же; верный код после них всё равно принимается (потолок — только на запись).
// Через час потолок восстанавливается.
func TestAcceptInviteFailureAuditCap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	for i := range 100 {
		if _, err := e.app.AcceptInvite(ctx, m.Email, otherCode(code), newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
			t.Fatalf("попытка %d: %v", i+1, err)
		}
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != inviteAuditCap {
		t.Fatalf("записей %d, ждали потолок %d", n, inviteAuditCap)
	}
	e.accept(m, code)
	e.clock.Advance(time.Hour)
	if _, err := e.app.AcceptInvite(ctx, m.Email, otherCode(code), newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatal(err)
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != inviteAuditCap+1 {
		t.Fatalf("через час записей %d", n)
	}
}

// logApp — сценарии с кольцом ring и журналом сервера в буфер.
func (e *env) logApp(ring *keys.Ring) (*app.App, *bytes.Buffer) {
	var buf bytes.Buffer
	d := e.deps
	d.Keys = ring
	d.Log = slog.New(slog.NewTextHandler(&buf, nil))
	return app.New(d), &buf
}

// Ключ кода убран из кольца раньше rekey (непредусмотренный отказ №1 ревью): принятие отвечает
// staff.invite_invalid (не 500) с записью неудачи, составитель письма — mail.ErrSkip (не «мёртвая»
// задача с повторами); оба — предупреждение в журнал сервера без кода.
func TestInviteUnreadableCode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	inv := e.liveInvite(m.ID)
	onlyNew, _ := newRing(t)
	a, logs := e.logApp(onlyNew)

	if _, err := a.AcceptInvite(ctx, m.Email, code, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("принятие с нечитаемым кодом: %v", err)
	}
	if n := len(e.audits("staff.invite_accept_failed")); n != 1 {
		t.Fatalf("записей неудачи %d", n)
	}
	if _, err := a.ComposeInvite(ctx, inv.ID, m.ID, staff.MailInvite); !errors.Is(err, mail.ErrSkip) {
		t.Fatalf("письмо с нечитаемым кодом: %v", err)
	}
	out := logs.String()
	if strings.Count(out, "level=WARN") != 2 || !strings.Contains(out, inv.ID.String()) {
		t.Fatalf("предупреждения:\n%s", out)
	}
	if strings.Contains(out, code) || strings.Contains(out, strings.ReplaceAll(code, "-", "")) || strings.Contains(out, m.Email) {
		t.Fatalf("секрет или почта в журнале сервера:\n%s", out)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE staff_id = $1`, m.ID); n != 0 {
		t.Fatalf("сессий %d", n)
	}
}
