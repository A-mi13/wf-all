package jobs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/jobs"
)

// fixture — настоящие письма всех видов, поставленные сценариями (аргументы — из river_job).
type fixture struct {
	ownerRU, ownerEN, offOwner, admin uuid.UUID
	newOwner, resetAdmin              uuid.UUID
	inviteCode, resetCode             string
	lockedSession                     uuid.UUID
	bootstrappedAudit                 uuid.UUID // запись аудита staff.bootstrapped — «запись другого действия»
}

func newFixture(e *env) fixture {
	e.t.Helper()
	ctx := context.Background()
	var f fixture
	f.ownerRU = e.member("owner@wf.example", "owner", "active", "ru")
	f.ownerEN = e.member("owner2@wf.example", "owner", "active", "en")
	f.offOwner = e.member("off@wf.example", "owner", "disabled", "ru")
	f.admin = e.member("admin@wf.example", "admin", "active", "ru")
	f.resetAdmin = e.member("reset@wf.example", "admin", "active", "ru")
	e.member("gone@wf.example", "admin", "active", "ru")
	locked := e.member("locked@wf.example", "admin", "active", "ru")
	var err error
	// U2: bootstrap — приглашение новой учётке и staff.bootstrapped каждому активному владельцу
	if f.inviteCode, err = e.app.OperatorBootstrap(ctx, "new.owner@wf.example", "Пётр Н."); err != nil {
		e.t.Fatal(err)
	}
	if err := e.owner.QueryRow(ctx, `SELECT id FROM staff_accounts WHERE email = 'new.owner@wf.example'`).Scan(&f.newOwner); err != nil {
		e.t.Fatal(err)
	}
	if err := e.owner.QueryRow(ctx, `SELECT id FROM audit_log WHERE action = 'staff.bootstrapped'`).Scan(&f.bootstrappedAudit); err != nil {
		e.t.Fatal(err)
	}
	// сброс доступа — письмо staff.access_reset с новым кодом
	if f.resetCode, err = e.app.OperatorResetAccess(ctx, "reset@wf.example"); err != nil {
		e.t.Fatal(err)
	}
	// U1: выключение админа оператором — staff.admin_disabled всем активным владельцам
	if err := e.app.OperatorDisable(ctx, "gone@wf.example"); err != nil {
		e.t.Fatal(err)
	}
	// staff.locked: учётка закрыта, ref — сессия, на которой это случилось
	f.lockedSession = id.New()
	e.exec(`UPDATE staff_accounts SET locked_until = $2 WHERE id = $1`, locked, t0.Add(time.Hour))
	e.exec(`INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, expires_at, ip, user_agent)
		VALUES ($1, $2, decode(repeat('ab', 32), 'hex'), 'mfa_pending', $3, $3, $4, '203.0.113.10', 'UA')`,
		f.lockedSession, locked, t0, t0.Add(5*time.Minute))
	return f
}

// Каждая ветка — свой составитель: приглашение и сброс — ComposeInvite (вид письма — назначение
// приглашения), staff.locked — ComposeLocked, U2 — ComposeBootstrapped, U1 — ComposeAdminDisabled. Письмо
// уходит получателю задачи с содержимым своего вида; таймаут задачи — минута (медленный SMTP не держит
// исполнителя дольше).
func TestMailWorkerSends(t *testing.T) {
	e := newEnv(t)
	f := newFixture(e)
	cases := []struct {
		name string
		args staff.MailArgs
		to   string
		want []string
	}{
		{"приглашение", e.mailOf(staff.MailInvite, f.newOwner), "new.owner@wf.example", []string{f.inviteCode, testOrigin + "/accept"}},
		{"сброс доступа", e.mailOf(staff.MailAccessReset, f.resetAdmin), "reset@wf.example", []string{f.resetCode}},
		{"bootstrap ru", e.mailOf(staff.MailBootstrapped, f.ownerRU), "owner@wf.example", []string{"new.owner@wf.example", "Оператор сервера"}},
		{"bootstrap en", e.mailOf(staff.MailBootstrapped, f.ownerEN), "owner2@wf.example", []string{"new.owner@wf.example", "server operator"}},
		{"выключен админ", e.mailOf(staff.MailAdminDisabled, f.ownerRU), "owner@wf.example", []string{"gone@wf.example"}},
		{"блокировка", staff.NewMailArgs(staff.MailLocked, f.lockedSession, f.admin), "admin@wf.example", []string{"locked@wf.example", "203.0.113.0/24"}},
	}
	subjects := map[string]string{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sent := &mail.Memory{}
			w := jobs.NewMailWorker(e.deps, sent)
			if got := w.Timeout(job(c.args)); got != time.Minute {
				t.Errorf("таймаут %s", got)
			}
			if err := w.Work(context.Background(), job(c.args)); err != nil {
				t.Fatalf("Work: %v", err)
			}
			msgs := sent.Messages()
			if len(msgs) != 1 || msgs[0].To != c.to {
				t.Fatalf("письма %+v, ждали одно для %s", msgs, c.to)
			}
			for _, w := range c.want {
				if !strings.Contains(msgs[0].Subject+"\n"+msgs[0].Text, w) {
					t.Errorf("в письме нет %q:\n%s\n%s", w, msgs[0].Subject, msgs[0].Text)
				}
			}
			subjects[c.name] = msgs[0].Subject
		})
	}
	// приглашение и сброс — разные письма (вид по назначению, а не один шаблон)
	if subjects["приглашение"] == subjects["сброс доступа"] {
		t.Fatalf("темы приглашения и сброса совпали: %q", subjects["приглашение"])
	}
}

// Письмо больше не нужно (§4.12, «Поправки» — MailWorker): неизвестный вид и mail.ErrSkip составителя —
// отмена задачи River (JobCancel: без повторов), письма нет. Случаи — по одному на проверку составителя:
// сущности нет (в т.ч. удалена чисткой между постановкой и исполнением), ref не того вида, получатель
// выключен или не той роли. Аргументы другой версии — обычная ошибка (повтор возьмёт экземпляр нового
// релиза), не отмена.
func TestMailWorkerArgs(t *testing.T) {
	e := newEnv(t)
	f := newFixture(e)
	invite := e.mailOf(staff.MailInvite, f.newOwner)
	disabled := e.mailOf(staff.MailAdminDisabled, f.ownerRU)
	cases := []struct {
		name string
		args staff.MailArgs
	}{
		{"неизвестный вид", staff.NewMailArgs("staff.future", f.newOwner, f.ownerRU)},
		{"пустой вид", staff.NewMailArgs("", f.newOwner, f.ownerRU)},
		{"приглашения нет (удалено чисткой)", staff.NewMailArgs(staff.MailInvite, uuid.New(), f.newOwner)},
		{"приглашение письмом сброса", staff.NewMailArgs(staff.MailAccessReset, invite.Ref, f.newOwner)},
		{"приглашение чужому получателю", staff.NewMailArgs(staff.MailInvite, invite.Ref, f.ownerRU)},
		{"bootstrap: нет учётки", staff.NewMailArgs(staff.MailBootstrapped, uuid.New(), f.ownerRU)},
		{"bootstrap: ref не владелец", staff.NewMailArgs(staff.MailBootstrapped, f.admin, f.ownerRU)},
		{"bootstrap: получатель выключен", staff.NewMailArgs(staff.MailBootstrapped, f.newOwner, f.offOwner)},
		{"bootstrap: получатель не владелец", staff.NewMailArgs(staff.MailBootstrapped, f.newOwner, f.admin)},
		{"admin_disabled: нет записи аудита", staff.NewMailArgs(staff.MailAdminDisabled, uuid.New(), f.ownerRU)},
		{"admin_disabled: запись другого действия", staff.NewMailArgs(staff.MailAdminDisabled, f.bootstrappedAudit, f.ownerRU)},
		{"admin_disabled: получатель выключен", staff.NewMailArgs(staff.MailAdminDisabled, disabled.Ref, f.offOwner)},
		{"admin_disabled: получатель не владелец", staff.NewMailArgs(staff.MailAdminDisabled, disabled.Ref, f.admin)},
		{"блокировка без сессии", staff.NewMailArgs(staff.MailLocked, uuid.New(), f.ownerRU)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sent := &mail.Memory{}
			err := jobs.NewMailWorker(e.deps, sent).Work(context.Background(), job(c.args))
			if !errors.Is(err, &river.JobCancelError{}) {
				t.Fatalf("err = %v, ждали отмену задачи (JobCancel)", err)
			}
			if n := len(sent.Messages()); n != 0 {
				t.Fatalf("отправлено писем: %d", n)
			}
		})
	}
	t.Run("версия аргументов 2", func(t *testing.T) {
		sent := &mail.Memory{}
		a := invite
		a.V = 2
		err := jobs.NewMailWorker(e.deps, sent).Work(context.Background(), job(a))
		if err == nil || errors.Is(err, &river.JobCancelError{}) {
			t.Fatalf("err = %v, ждали обычную ошибку (повтор)", err)
		}
		if n := len(sent.Messages()); n != 0 {
			t.Fatalf("отправлено писем: %d", n)
		}
	})
}

// failingSender — SMTP недоступен.
type failingSender struct{ err error }

func (s failingSender) Send(context.Context, mail.Message) error { return s.err }

// Ошибка SMTP — обычная ошибка задачи: River повторит (MaxAttempts 10), письмо не теряется и не отменяется.
func TestMailWorkerSMTPErrorRetries(t *testing.T) {
	e := newEnv(t)
	f := newFixture(e)
	errSMTP := errors.New("mail: отправка: соединение отклонено")
	err := jobs.NewMailWorker(e.deps, failingSender{errSMTP}).Work(context.Background(), job(e.mailOf(staff.MailInvite, f.newOwner)))
	if !errors.Is(err, errSMTP) || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("err = %v, ждали ошибку SMTP без отмены", err)
	}
}

// Журнал сервера (страж): отмены и ошибки пишутся с видом письма и id, без почты и кодов приглашений.
func TestMailWorkerLogHasNoSecrets(t *testing.T) {
	e := newEnv(t)
	f := newFixture(e)
	ctx := context.Background()
	w := jobs.NewMailWorker(e.deps, &mail.Memory{})
	for _, a := range e.mails() {
		if err := w.Work(ctx, job(a)); err != nil {
			t.Fatalf("%s: %v", a.Mail, err)
		}
	}
	for _, a := range []staff.MailArgs{
		staff.NewMailArgs("staff.future", f.newOwner, f.ownerRU),
		staff.NewMailArgs(staff.MailBootstrapped, f.newOwner, f.offOwner),
	} {
		_ = w.Work(ctx, job(a))
	}
	_ = jobs.NewMailWorker(e.deps, failingSender{errors.New("smtp: 550 rejected owner@wf.example")}).
		Work(ctx, job(e.mailOf(staff.MailBootstrapped, f.ownerRU)))
	log := e.log.String()
	// не вхолостую: отмены в журнале есть
	if !strings.Contains(log, "staff.future") || !strings.Contains(log, staff.MailBootstrapped) {
		t.Fatalf("в журнале нет отмен:\n%s", log)
	}
	for _, secret := range []string{"@wf.example", f.inviteCode, f.resetCode, "Пётр Н."} {
		if strings.Contains(log, secret) {
			t.Errorf("в журнале %q:\n%s", secret, log)
		}
	}
}
