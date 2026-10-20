// Package jobs — фон модуля staff в admin-api (D3, P27): исполнитель писем staff.mail на очереди staff и
// тикер чистки (Cleaner). Всё о сотрудниках исполняет admin-api под ролью БД admin: у воркера нет ни ключей,
// ни прав на staff_*. Сборка (cmd/admin-api) регистрирует MailWorker в исполняющем River-клиенте admin-api
// (AddWorkers) и запускает Cleaner.Run в фоне сервиса.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/platform/mail"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

// mailTimeout — срок одной задачи письма: составление и SMTP (у mail.SMTP свой таймаут 20 с на соединение);
// медленный SMTP не держит исполнителя дольше — задача уйдёт в повтор.
const mailTimeout = time.Minute

// errUnknownKind — вид письма, которого этот admin-api не знает.
var errUnknownKind = errors.New("staff: неизвестный вид письма")

// MailWorker — исполнитель задачи staff.mail (§4.10, §4.12): по виду письма зовёт составителя сценариев
// (адрес, язык и код — из базы по id аргументов) и отправляет письмо SMTP admin-api (ADMIN_MAIL_*).
type MailWorker struct {
	river.WorkerDefaults[staff.MailArgs]
	app    *app.App
	sender mail.Sender
	log    *slog.Logger
}

// NewMailWorker — исполнитель писем на зависимостях admin-api и её SMTP.
func NewMailWorker(d staff.Deps, s mail.Sender) *MailWorker {
	return &MailWorker{app: app.New(d), sender: s, log: d.Log}
}

// AddWorkers — исполнители очереди staff в реестр исполняющего клиента admin-api.
func AddWorkers(w *river.Workers, d staff.Deps, s mail.Sender) {
	river.AddWorker(w, NewMailWorker(d, s))
}

func (*MailWorker) Timeout(*river.Job[staff.MailArgs]) time.Duration { return mailTimeout }

// Work — письмо одной задачи. Письмо больше не нужно (mail.ErrSkip составителя: сущности нет — в т.ч.
// удалена чисткой, ref не того вида, получатель выключен или не той роли, код нечитаем) и неизвестный вид —
// отмена задачи без повторов (river.JobCancel): повторы этого не исправят. Аргументы другой версии —
// обычная ошибка: повтор возьмёт экземпляр релиза, который их понимает. Ошибка базы и SMTP — обычная
// ошибка, River повторит (MaxAttempts 10). В журнал — только вид письма и id (адресов и кодов в нём нет).
func (w *MailWorker) Work(ctx context.Context, job *river.Job[staff.MailArgs]) error {
	a := job.Args
	if a.V != 1 {
		return fmt.Errorf("staff: версия аргументов письма %d не поддерживается этим admin-api", a.V)
	}
	m, err := w.compose(ctx, a)
	if errors.Is(err, mail.ErrSkip) || errors.Is(err, errUnknownKind) {
		level := slog.LevelInfo
		if errors.Is(err, errUnknownKind) {
			level = slog.LevelWarn
		}
		w.log.Log(ctx, level, "staff: письмо не отправлено, задача отменена", "kind", a.Mail, "job_id", job.ID,
			"ref", a.Ref.String(), "recipient", a.Recipient.String(), "reason", err.Error())
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	return w.sender.Send(ctx, m)
}

// compose — письмо вида a.Mail; составитель проверяет аргументы сам (mail.ErrSkip).
func (w *MailWorker) compose(ctx context.Context, a staff.MailArgs) (mail.Message, error) {
	switch a.Mail {
	case staff.MailInvite, staff.MailAccessReset:
		return w.app.ComposeInvite(ctx, a.Ref, a.Recipient, a.Mail)
	case staff.MailLocked:
		return w.app.ComposeLocked(ctx, a.Ref, a.Recipient)
	case staff.MailBootstrapped:
		return w.app.ComposeBootstrapped(ctx, a.Ref, a.Recipient)
	case staff.MailAdminDisabled:
		return w.app.ComposeAdminDisabled(ctx, a.Ref, a.Recipient)
	}
	return mail.Message{}, fmt.Errorf("%w %q", errUnknownKind, a.Mail)
}
