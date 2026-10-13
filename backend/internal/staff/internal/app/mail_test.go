package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/mail"
)

// lockedSession — блокировка учётки m пятью неудачами входа с адреса 203.0.113.10; id сессии, на
// которой она закрылась (ref письма staff.locked).
func (e *env) lockedSession(m member) uuid.UUID {
	e.t.Helper()
	s := e.pending(m)
	for range 5 {
		if _, err := e.app.VerifyMFA(context.Background(), s, "000000", "", metaWeb); err == nil {
			e.t.Fatal("неверный код принят")
		}
	}
	return s.SessionID
}

// ComposeLocked (§4.12): кто закрыт (имя и почта), когда, IP маскирован до /24 — на языке
// получателя; staff_accounts.locale нормализуется (en-US → en, неизвестный — ru).
func TestComposeLocked(t *testing.T) {
	e := newEnv(t)
	ruOwner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	enAdmin := e.addMember("admin@wf.example", memberOpts{Role: "admin", Locale: "en"})
	m := e.addMember("mod@wf.example", memberOpts{Role: "admin", Name: "Пётр К."})
	ref := e.lockedSession(m)

	ru, err := e.app.ComposeLocked(context.Background(), ref, ruOwner.ID)
	if err != nil {
		t.Fatal(err)
	}
	en, err := e.app.ComposeLocked(context.Background(), ref, enAdmin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ru.To != ruOwner.Email || en.To != enAdmin.Email {
		t.Fatalf("адресаты %q %q", ru.To, en.To)
	}
	for _, msg := range []mail.Message{ru, en} {
		for _, want := range []string{"Пётр К.", "mod@wf.example", "203.0.113.0/24", "2026-10-13 12:00 UTC", testOrigin} {
			if !strings.Contains(msg.Subject+msg.Text, want) {
				t.Errorf("в письме %q нет %q:\n%s", msg.Subject, want, msg.Text)
			}
		}
		if strings.Contains(msg.Text, "203.0.113.10") {
			t.Errorf("IP не маскирован:\n%s", msg.Text)
		}
	}
	if !strings.Contains(ru.Text, "закрыт") || !strings.Contains(en.Text, "locked") || ru.Subject == en.Subject {
		t.Fatalf("языки писем:\nru: %s\n%s\nen: %s\n%s", ru.Subject, ru.Text, en.Subject, en.Text)
	}

	// en-US — английский (остаток фундамента: нормализация локали письма)
	e.exec(`ALTER TABLE staff_accounts DROP CONSTRAINT staff_accounts_locale_check`)
	e.exec(`UPDATE staff_accounts SET locale = 'en-US' WHERE id = $1`, ruOwner.ID)
	us, err := e.app.ComposeLocked(context.Background(), ref, ruOwner.ID)
	if err != nil || us.Subject != en.Subject {
		t.Fatalf("en-US: %q, ждали %q (%v)", us.Subject, en.Subject, err)
	}
	e.exec(`UPDATE staff_accounts SET locale = 'xx' WHERE id = $1`, ruOwner.ID)
	if xx, err := e.app.ComposeLocked(context.Background(), ref, ruOwner.ID); err != nil || xx.Subject != ru.Subject {
		t.Fatalf("неизвестный язык: %q (%v)", xx.Subject, err)
	}

	// сессию неудачи мог погасить и не lockOut (например, заменил вход): письмо опирается на
	// учётку и её locked_until, а не на причину гашения сессии
	e.exec(`UPDATE staff_sessions SET revoked_reason = 'replaced' WHERE id = $1`, ref)
	rep, err := e.app.ComposeLocked(context.Background(), ref, enAdmin.ID)
	if err != nil || rep.Text != en.Text {
		t.Fatalf("сессия replaced: %v\n%s", err, rep.Text)
	}
}

// Письмо не нужно — mail.ErrSkip (задача отменяется): нет сессии или учётку не закрывали
// (locked_until пуст; причину гашения сессии письмо не смотрит), получатель выключен, не владелец и не
// админ или не существует.
func TestComposeLockedSkips(t *testing.T) {
	e := newEnv(t)
	owner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	support := e.addMember("support@wf.example", memberOpts{Role: "support"})
	off := e.addMember("off@wf.example", memberOpts{Role: "owner", Status: "disabled"})
	m := e.addMember("mod@wf.example", memberOpts{})
	ref := e.lockedSession(m)
	other := e.addMember("other@wf.example", memberOpts{})
	logout := e.load(e.signIn(other))
	if err := e.app.Logout(context.Background(), logout); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name           string
		ref, recipient uuid.UUID
	}{
		{"нет сессии", uuid.New(), owner.ID},
		{"учётку не закрывали", logout.SessionID, owner.ID},
		{"получатель — поддержка", ref, support.ID},
		{"получатель выключен", ref, off.ID},
		{"нет получателя", ref, uuid.New()},
	}
	for _, c := range cases {
		if _, err := e.app.ComposeLocked(context.Background(), c.ref, c.recipient); !errors.Is(err, mail.ErrSkip) {
			t.Errorf("%s: %v, ждали mail.ErrSkip", c.name, err)
		}
	}
}
