package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/mail"
)

// writeDisabledAudit — запись аудита staff.disabled о выключении учётки object с причиной reason от
// автора label (оператор — roleOperator); id записи.
func (e *menv) writeDisabledAudit(object uuid.UUID, action, reason string, operator bool) uuid.UUID {
	e.t.Helper()
	ref := id.New()
	entry := audit.Entry{ID: ref, ActorRole: "operator", ActorLabel: "operator", Action: action,
		ObjectType: "staff_account", ObjectID: object, Reason: reason}
	if !operator {
		var by uuid.UUID
		if err := e.owner.QueryRow(context.Background(), `SELECT a.id FROM staff_accounts a JOIN staff_roles r ON r.staff_id = a.id
			WHERE r.role = 'owner' ORDER BY a.id LIMIT 1`).Scan(&by); err != nil {
			e.t.Fatal(err)
		}
		entry.ActorStaffID, entry.ActorRole, entry.ActorLabel = by, "owner", "Анна В."
	}
	if err := db.InTx(context.Background(), e.owner, func(ctx context.Context, _ pgx.Tx) error {
		return audit.Write(ctx, entry)
	}); err != nil {
		e.t.Fatal(err)
	}
	return ref
}

// ComposeAdminDisabled (U1, табличный): кто выключил, кого (имя и почта), когда и причина — переведённая
// метка кода (часть Reason до ": "); комментарий в письмо не попадает никогда; operator — «оператор
// сервера»; неизвестный код — письмо без строки причины; ru и en по языку получателя.
func TestComposeAdminDisabled(t *testing.T) {
	e := newManageEnv(t)
	ru := e.addMember("owner.ru@wf.example", memberOpts{Role: "owner", Name: "Анна В."})
	en := e.addMember("owner.en@wf.example", memberOpts{Role: "owner", Locale: "en"})
	x := e.addMember("admin.x@wf.example", memberOpts{Role: "admin", Name: "Пётр К.", Status: "disabled"})
	cases := []struct {
		name, reason       string
		operator           bool
		wantRu, wantEn     string // строка причины; "" — строки причины нет
		wantActor, actorEn string
	}{
		{"left_team", "left_team", false, "Причина: Ушёл из команды", "Reason: Left the team", "Анна В.", "Анна В."},
		{"security с комментарием", "security: утёк ноутбук ivan@mail.ru", false, "Причина: Безопасность", "Reason: Security", "Анна В.", "Анна В."},
		{"оператор", "operator", true, "Причина: оператор сервера", "Reason: server operator", "оператор сервера", "server operator"},
		{"неизвестный код", "future_code: что-то", false, "", "", "Анна В.", "Анна В."},
	}
	for _, tc := range cases {
		ref := e.writeDisabledAudit(x.ID, "staff.disabled", tc.reason, tc.operator)
		var at time.Time
		if err := e.owner.QueryRow(context.Background(), `SELECT created_at FROM audit_log WHERE id = $1`, ref).Scan(&at); err != nil {
			t.Fatal(err)
		}
		when := at.UTC().Format("2006-01-02 15:04 UTC")
		for _, rc := range []struct {
			to                member
			wantReason, actor string
			reasonWord        string
		}{{ru, tc.wantRu, tc.wantActor, "Причина"}, {en, tc.wantEn, tc.actorEn, "Reason"}} {
			msg, err := e.app.ComposeAdminDisabled(context.Background(), ref, rc.to.ID)
			if err != nil {
				t.Fatalf("%s → %s: %v", tc.name, rc.to.Email, err)
			}
			text := msg.Subject + "\n" + msg.Text
			if msg.To != rc.to.Email {
				t.Fatalf("%s: адресат %q", tc.name, msg.To)
			}
			for _, want := range []string{"Пётр К.", "admin.x@wf.example", rc.actor, when} {
				if !strings.Contains(text, want) {
					t.Errorf("%s → %s: нет %q:\n%s", tc.name, rc.to.Email, want, text)
				}
			}
			if rc.wantReason != "" && !strings.Contains(text, rc.wantReason) {
				t.Errorf("%s → %s: нет причины %q:\n%s", tc.name, rc.to.Email, rc.wantReason, text)
			}
			if rc.wantReason == "" && strings.Contains(text, rc.reasonWord) {
				t.Errorf("%s → %s: строка причины при неизвестном коде:\n%s", tc.name, rc.to.Email, text)
			}
			for _, secret := range []string{"ноутбук", "ivan@mail.ru", "что-то", "future_code"} {
				if strings.Contains(text, secret) {
					t.Errorf("%s: в письме %q:\n%s", tc.name, secret, text)
				}
			}
		}
	}
}

// ComposeAdminDisabled — mail.ErrSkip: ref не запись аудита, запись другого действия, получатель
// выключен, не владелец или не существует.
func TestComposeAdminDisabledSkips(t *testing.T) {
	e := newManageEnv(t)
	owner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	off := e.addMember("off@wf.example", memberOpts{Role: "owner", Status: "disabled"})
	admin := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	x := e.addMember("x@wf.example", memberOpts{Role: "admin", Status: "disabled"})
	ref := e.writeDisabledAudit(x.ID, "staff.disabled", "other", false)
	other := e.writeDisabledAudit(x.ID, "staff.access_reset", "other", false)
	if _, err := e.app.ComposeAdminDisabled(context.Background(), ref, owner.ID); err != nil {
		t.Fatalf("контроль: %v", err)
	}
	for _, c := range []struct {
		name           string
		ref, recipient uuid.UUID
	}{
		{"нет записи", uuid.New(), owner.ID},
		{"другое действие", other, owner.ID},
		{"получатель выключен", ref, off.ID},
		{"получатель — админ", ref, admin.ID},
		{"нет получателя", ref, uuid.New()},
	} {
		if _, err := e.app.ComposeAdminDisabled(context.Background(), c.ref, c.recipient); !errors.Is(err, mail.ErrSkip) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
