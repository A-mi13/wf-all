package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/staff"
)

// inviteLocaleIn — приглашение support на почту email с языком locale (nil — язык приглашающего).
func inviteLocaleIn(email string, locale *string) staff.InviteInput {
	return staff.InviteInput{Email: email, DisplayName: "Приглашённый", Role: "support", Locale: locale}
}

// payloadLocale — поле locale payload запроса (нет поля — "").
func payloadLocale(t *testing.T, raw []byte) string {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	s, _ := p["locale"].(string)
	return s
}

// invitedAfterLocale — after.locale записи staff.invited учётки (нет ключа — "").
func (e *env) invitedAfterLocale(staffID uuid.UUID) string {
	e.t.Helper()
	var l *string
	if err := e.owner.QueryRow(context.Background(), `SELECT after->>'locale' FROM audit_log
		WHERE action = 'staff.invited' AND object_id = $1`, staffID).Scan(&l); err != nil {
		e.t.Fatal(err)
	}
	if l == nil {
		return ""
	}
	return *l
}

// Язык новичка задаёт приглашающий (IL2, §4.3): нет поля — язык инициатора на момент запроса (снимок в payload:
// смена языка инициатора до подтверждения его не меняет); явный язык — он, а не язык инициатора.
func TestInviteLocaleFromInitiator(t *testing.T) {
	t.Parallel()
	t.Run("умолчание — язык инициатора на момент запроса", func(t *testing.T) {
		t.Parallel()
		e := newManageEnv(t)
		a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner", Locale: "en"}))
		b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
		ap, err := e.app.Invite(context.Background(), a, inviteLocaleIn("new@wf.example", nil), metaWeb)
		if err != nil {
			t.Fatal(err)
		}
		if got := payloadLocale(t, ap.Payload); got != "en" {
			t.Fatalf("payload locale %q", got)
		}
		e.exec(`UPDATE staff_accounts SET locale = 'ru' WHERE id = $1`, a.StaffID)
		e.mustApprove(b, ap.ID)
		if got := e.localeOf(e.accountID("new@wf.example")); got != "en" {
			t.Fatalf("язык учётки %q", got)
		}
		// m1 ревью IL2: выбор языка — и в записи staff.invited (след после PurgeDecided)
		if got := e.invitedAfterLocale(e.accountID("new@wf.example")); got != "en" {
			t.Fatalf("after.locale staff.invited %q", got)
		}
	})
	t.Run("явный язык", func(t *testing.T) {
		t.Parallel()
		e := newManageEnv(t)
		a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
		b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
		ap, err := e.app.Invite(context.Background(), a, inviteLocaleIn("new@wf.example", ptr("en")), metaWeb)
		if err != nil {
			t.Fatal(err)
		}
		e.mustApprove(b, ap.ID)
		if got := e.localeOf(e.accountID("new@wf.example")); got != "en" {
			t.Fatalf("язык учётки %q", got)
		}
		if got := e.invitedAfterLocale(e.accountID("new@wf.example")); got != "en" {
			t.Fatalf("after.locale staff.invited %q", got)
		}
	})
}

// Незнакомый язык при прямом вызове сценария — 400 validation.failed по body.locale (enum), как SetLocale и
// принятие; запрос не создан.
func TestInviteLocaleValidation(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	for _, bad := range []string{"", "EN", "en-US", "de", " en"} {
		_, err := e.app.Invite(context.Background(), a, inviteLocaleIn("new@wf.example", ptr(bad)), metaWeb)
		wantFieldError(t, err, "body.locale", "enum")
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("запросов %d", n)
	}
}

// Битый язык в payload приглашения — бизнес-отказ ErrStale, ничего не создано (как роль и города), а не 500 от
// CHECK staff_accounts_locale_check.
func TestExecInviteRejectsLocale(t *testing.T) {
	t.Parallel()
	for _, bad := range []any{"de", "EN", ""} {
		e := newInvitePayloadEnv(t)
		e.patch(map[string]any{"locale": bad})
		res, err := e.approve(e.b, e.ap.ID)
		e.wantStaleNothingCreated(res, err, "")
	}
}

// Запрос, поданный до релиза IL2 (в payload нет locale, живой стенд), исполняется: новая учётка — язык по
// умолчанию базы, повторное приглашение выключенной — язык прежний.
func TestExecInviteLegacyPayloadWithoutLocale(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner", Locale: "en"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("x@wf.example", memberOpts{Role: "support", Status: "disabled", Locale: "en"})
	fresh, err := e.app.Invite(context.Background(), a, inviteLocaleIn("new@wf.example", nil), metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	back, err := e.app.Invite(context.Background(), a, inviteLocaleIn(x.Email, ptr("ru")), metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE approvals SET payload = payload - 'locale' WHERE id = ANY($1)`, []uuid.UUID{fresh.ID, back.ID})
	e.mustApprove(b, fresh.ID)
	e.mustApprove(b, back.ID)
	if got := e.localeOf(e.accountID("new@wf.example")); got != "ru" {
		t.Fatalf("новая учётка: язык %q", got)
	}
	if got := e.invitedAfterLocale(e.accountID("new@wf.example")); got != "" {
		t.Fatalf("новая учётка без языка в payload: after.locale %q", got)
	}
	if got := e.localeOf(x.ID); got != "en" {
		t.Fatalf("выключенная: язык %q", got)
	}
}

// Повторное приглашение выключенной учётки (сброс доступа, §4.3 п.2): язык перезаписан выбранным приглашающим,
// в записи staff.invited — было/стало; тот же язык — ключей языка в записи нет.
func TestInviteDisabledLocale(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, was string
		changed   bool
	}{{"сменился", "ru", true}, {"тот же", "en", false}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newManageEnv(t)
			a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner", Locale: "en"}))
			b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
			x := e.addMember("x@wf.example", memberOpts{Role: "support", Status: "disabled", Locale: c.was})
			ap, err := e.app.Invite(context.Background(), a, inviteLocaleIn(x.Email, nil), metaWeb)
			if err != nil {
				t.Fatal(err)
			}
			e.mustApprove(b, ap.ID)
			if got := e.localeOf(x.ID); got != "en" {
				t.Fatalf("язык %q", got)
			}
			var before, after *string
			if err := e.owner.QueryRow(context.Background(), `SELECT before->>'locale', after->>'locale' FROM audit_log
				WHERE action = 'staff.invited' AND object_id = $1`, x.ID).Scan(&before, &after); err != nil {
				t.Fatal(err)
			}
			switch {
			case c.changed && (before == nil || *before != "ru" || after == nil || *after != "en"):
				t.Fatalf("было/стало %v %v", before, after)
			case !c.changed && (before != nil || after != nil):
				t.Fatalf("язык не менялся, а в записи %v %v", before, after)
			}
		})
	}
}

// Повторные отправки — админом, самим сотрудником, оператором — письмо на языке учётки со ссылкой
// /accept?lang=<язык учётки> (почты в адресе нет).
func TestInviteResendKeepsLocale(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		resend func(e *env, m member) error
	}{
		{"админ", func(e *env, m member) error {
			owner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
			return e.app.ResendInvite(context.Background(), e.load(e.signIn(owner)), m.ID, metaWeb)
		}},
		{"сам", func(e *env, m member) error {
			return e.app.ResendInviteSelf(context.Background(), m.Email, metaWeb)
		}},
		{"оператор", func(e *env, m member) error {
			_, err := e.app.OperatorInviteResend(context.Background(), m.Email)
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			m, _ := e.invitee("nina.k@wf.example", memberOpts{Role: "support", Name: "Nina K.", Locale: "en"}, purposeInvite)
			e.clock.Advance(61 * time.Second)
			if err := c.resend(e, m); err != nil {
				t.Fatal(err)
			}
			// m2 ревью IL2: письмо — из аргументов поставленной задачи staff.mail (как его соберёт MailWorker),
			// а не из живого приглашения с заданным видом
			if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail'
				AND (args->>'recipient')::uuid = $1`, m.ID); n != 2 {
				t.Fatalf("задач staff.mail учётки %d, ждали 2 (приглашение и повторная отправка)", n)
			}
			var args staff.MailArgs
			if err := e.owner.QueryRow(context.Background(), `SELECT args FROM river_job WHERE kind = 'staff.mail'
				AND (args->>'recipient')::uuid = $1 ORDER BY id DESC LIMIT 1`, m.ID).Scan(&args); err != nil {
				t.Fatal(err)
			}
			msg, err := e.app.ComposeInvite(context.Background(), args.Ref, args.Recipient, args.Mail)
			if err != nil {
				t.Fatal(err)
			}
			txt := msg.Subject + "\n" + msg.Text
			if !strings.Contains(txt, testOrigin+"/accept?lang=en,") || !strings.Contains(txt, "Hello, Nina K.") {
				t.Fatalf("письмо не на языке учётки:\n%s", txt)
			}
		})
	}
}
