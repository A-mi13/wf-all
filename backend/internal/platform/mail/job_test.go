package mail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/mail"
)

type composer func(context.Context, uuid.UUID) (mail.Message, error)

func (f composer) Compose(ctx context.Context, ref uuid.UUID) (mail.Message, error) {
	return f(ctx, ref)
}

func TestRegistry(t *testing.T) {
	r := mail.NewRegistry()
	c := composer(func(context.Context, uuid.UUID) (mail.Message, error) { return mail.Message{}, nil })
	if err := r.Add("identity.signup_code", c); err != nil {
		t.Fatal(err)
	}
	if r.Add("identity.signup_code", c) == nil {
		t.Fatal("повтор вида принят")
	}
	for _, bad := range []string{"", "signup", "Identity.code", "identity."} {
		if r.Add(bad, c) == nil {
			t.Errorf("вид %q принят", bad)
		}
	}
}

func TestSendWorker(t *testing.T) {
	ctx := context.Background()
	ref := uuid.New()
	var sent mail.Memory
	r := mail.NewRegistry()
	_ = r.Add("identity.signup_code", composer(func(_ context.Context, got uuid.UUID) (mail.Message, error) {
		if got != ref {
			t.Errorf("ref = %v", got)
		}
		return mail.Message{To: "user@example.com", Subject: "Код"}, nil
	}))
	_ = r.Add("identity.expired", composer(func(context.Context, uuid.UUID) (mail.Message, error) {
		return mail.Message{}, mail.ErrSkip
	}))
	workers := river.NewWorkers()
	mail.AddWorkers(workers, r, &sent)
	w := mail.NewSendWorker(r, &sent)

	if err := w.Work(ctx, &river.Job[mail.SendArgs]{Args: mail.SendArgs{V: 1, Mail: "identity.signup_code", Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	if msgs := sent.Messages(); len(msgs) != 1 || msgs[0].To != "user@example.com" {
		t.Fatalf("отправлено: %+v", msgs)
	}
	// письмо больше не нужно — задача отменяется, а не повторяется
	err := w.Work(ctx, &river.Job[mail.SendArgs]{Args: mail.SendArgs{V: 1, Mail: "identity.expired", Ref: ref}})
	if !errors.Is(err, mail.ErrSkip) {
		t.Fatalf("ErrSkip: %v", err)
	}
	// именно отмена River (JobCancel), а не обычная ошибка: иначе River повторит задачу 10 раз
	if !errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("задача не отменена: %v", err)
	}
	// вид не зарегистрирован этим воркером (старый релиз) — обычная ошибка: River повторит
	if err := w.Work(ctx, &river.Job[mail.SendArgs]{Args: mail.SendArgs{V: 1, Mail: "identity.new_kind", Ref: ref}}); err == nil || errors.Is(err, mail.ErrSkip) || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("неизвестный вид — обычная ошибка (не отмена): %v", err)
	}
	// версия аргументов от более нового релиза — тоже обычная ошибка: повтор возьмёт новый воркер
	n := len(sent.Messages())
	if err := w.Work(ctx, &river.Job[mail.SendArgs]{Args: mail.SendArgs{V: 2, Mail: "identity.signup_code", Ref: ref}}); err == nil || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("V=2: %v", err)
	}
	if len(sent.Messages()) != n {
		t.Fatal("письмо отправлено по аргументам неизвестной версии")
	}
	if opts := (mail.SendArgs{}).InsertOpts(); opts.Queue != "mail" || opts.MaxAttempts != 10 {
		t.Fatalf("InsertOpts: %+v", opts)
	}
}
