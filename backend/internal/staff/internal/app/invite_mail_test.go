package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/mail"
	"wf/backend/internal/staff"
)

// ComposeInvite (§4.3, §4.12): письма staff.invite и staff.access_reset на языке учётки — имя, код,
// ссылка ADMIN_ORIGIN/accept без почты в адресе, срок кода.
func TestComposeInvite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ru, ruCode := e.invitee("ivan.p@wf.example", memberOpts{Role: "support", Name: "Иван П."}, purposeInvite)
	en, enCode := e.invitee("nina.k@wf.example", memberOpts{Role: "support", Name: "Nina K.", Locale: "en"}, purposeReset)
	ruInv, enInv := e.liveInvite(ru.ID), e.liveInvite(en.ID)

	inv, err := e.app.ComposeInvite(ctx, ruInv.ID, ru.ID, staff.MailInvite)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := e.app.ComposeInvite(ctx, enInv.ID, en.ID, staff.MailAccessReset)
	if err != nil {
		t.Fatal(err)
	}
	if inv.To != ru.Email || reset.To != en.Email {
		t.Fatalf("адресаты %q %q", inv.To, reset.To)
	}
	for _, c := range []struct {
		msg        mail.Message
		code, name string
		lang       string
	}{{inv, ruCode, "Иван П.", "Приглашение"}, {reset, enCode, "Nina K.", "access reset"}} {
		txt := c.msg.Subject + "\n" + c.msg.Text
		for _, want := range []string{c.code, c.name, testOrigin + "/accept", "2026-10-14 12:00 UTC", c.lang} {
			if !strings.Contains(txt, want) {
				t.Errorf("в письме нет %q:\n%s", want, txt)
			}
		}
		if strings.Contains(txt, "/accept?") || strings.Contains(txt, "accept/") {
			t.Errorf("в ссылке лишнее:\n%s", txt)
		}
	}
}

// ComposeInvite отменяет письмо (mail.ErrSkip): не тот получатель, не тот вид, погашено (новым
// приглашением), срок истёк, приглашение принято-и-закрыто, нет приглашения.
func TestComposeInviteSkips(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, _ := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	live := e.liveInvite(m.ID)
	other, _ := e.invitee("nina.k@wf.example", memberOpts{Role: "support"}, purposeInvite)

	old, _ := e.invitee("old@wf.example", memberOpts{Role: "support"}, purposeInvite)
	oldRef := e.liveInvite(old.ID).ID
	e.issue(old.ID, purposeInvite)

	done, doneCode := e.invitee("done@wf.example", memberOpts{Role: "support"}, purposeInvite)
	doneRef := e.liveInvite(done.ID).ID
	_, s := e.accept(done, doneCode)
	e.completeSetup(s)

	cases := []struct {
		name           string
		ref, recipient uuid.UUID
		kind           string
	}{
		{"не тот получатель", live.ID, other.ID, staff.MailInvite},
		{"не тот вид", live.ID, m.ID, staff.MailAccessReset},
		{"погашено", oldRef, old.ID, staff.MailInvite},
		{"закрыто ack", doneRef, done.ID, staff.MailInvite},
		{"нет приглашения", uuid.New(), m.ID, staff.MailInvite},
	}
	for _, c := range cases {
		if _, err := e.app.ComposeInvite(ctx, c.ref, c.recipient, c.kind); !errors.Is(err, mail.ErrSkip) {
			t.Errorf("%s: %v, ждали mail.ErrSkip", c.name, err)
		}
	}
	if _, err := e.app.ComposeInvite(ctx, live.ID, m.ID, staff.MailInvite); err != nil {
		t.Fatalf("живое: %v", err)
	}
	e.clock.Advance(24 * time.Hour)
	if _, err := e.app.ComposeInvite(ctx, live.ID, m.ID, staff.MailInvite); !errors.Is(err, mail.ErrSkip) {
		t.Fatalf("истёкшее: %v", err)
	}
}

// ComposeBootstrapped (U2): кого добавил оператор (имя и почта новой учётки), когда — на языке
// получателя; получатель — активный владелец, ref — учётка владельца, созданная оператором, иначе
// mail.ErrSkip.
func TestComposeBootstrapped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ruOwner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	enOwner := e.addMember("owner2@wf.example", memberOpts{Role: "owner", Locale: "en"})
	off := e.addMember("off@wf.example", memberOpts{Role: "owner", Status: "disabled"})
	admin := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	e.clock.Advance(90 * time.Minute)
	if _, err := e.app.OperatorBootstrap(ctx, "new.owner@wf.example", "Пётр Н."); err != nil {
		t.Fatal(err)
	}
	var ref uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT id FROM staff_accounts WHERE email = 'new.owner@wf.example'`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	ru, err := e.app.ComposeBootstrapped(ctx, ref, ruOwner.ID)
	if err != nil {
		t.Fatal(err)
	}
	en, err := e.app.ComposeBootstrapped(ctx, ref, enOwner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ru.To != ruOwner.Email || en.To != enOwner.Email || ru.Subject == en.Subject {
		t.Fatalf("письма %q %q / %q %q", ru.To, ru.Subject, en.To, en.Subject)
	}
	for _, c := range []struct {
		msg      mail.Message
		operator string
	}{{ru, "Оператор сервера"}, {en, "server operator"}} {
		txt := c.msg.Subject + "\n" + c.msg.Text
		for _, want := range []string{"Пётр Н.", "new.owner@wf.example", "2026-10-13 13:30 UTC", c.operator, testOrigin} {
			if !strings.Contains(txt, want) {
				t.Errorf("в письме нет %q:\n%s", want, txt)
			}
		}
	}
	invited, _ := e.invitee("support@wf.example", memberOpts{Role: "support"}, purposeInvite)
	cases := []struct {
		name           string
		ref, recipient uuid.UUID
	}{
		{"получатель выключен", ref, off.ID},
		{"получатель не владелец", ref, admin.ID},
		{"нет получателя", ref, uuid.New()},
		{"ref не владелец", invited.ID, ruOwner.ID},
		{"ref создан не оператором", enOwner.ID, ruOwner.ID},
		{"нет учётки", uuid.New(), ruOwner.ID},
	}
	e.exec(`UPDATE staff_accounts SET created_by = $1 WHERE id = $2`, ruOwner.ID, enOwner.ID)
	for _, c := range cases {
		if _, err := e.app.ComposeBootstrapped(ctx, c.ref, c.recipient); !errors.Is(err, mail.ErrSkip) {
			t.Errorf("%s: %v, ждали mail.ErrSkip", c.name, err)
		}
	}
}
