package mail

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
)

// ErrSkip — письмо больше не нужно (код истёк, аккаунт удалён): задача отменяется без повторов.
var ErrSkip = errors.New("mail: письмо больше не нужно")

// Composer — письмо одного вида: модуль по ref находит адрес и язык получателя (адресов в
// задаче нет, §6.9) и собирает текст из серверных локалей (i18n).
type Composer interface {
	Compose(ctx context.Context, ref uuid.UUID) (Message, error)
}

var kindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Registry — виды писем этого воркера; модули добавляют свои при старте.
type Registry struct {
	m map[string]Composer
}

func NewRegistry() *Registry { return &Registry{m: map[string]Composer{}} }

func (r *Registry) Add(kind string, c Composer) error {
	if !kindRe.MatchString(kind) {
		return fmt.Errorf("mail: вид %q — нужен <модуль>.<письмо>", kind)
	}
	if _, dup := r.m[kind]; dup {
		return fmt.Errorf("mail: вид %q уже зарегистрирован", kind)
	}
	r.m[kind] = c
	return nil
}

// SendArgs — задача очереди mail: вид письма и id получателя или кода — не адрес.
// Доставка «хотя бы один раз»: если SMTP принял письмо, а задача не успела завершиться,
// повтор отправит дубль — письма должны быть безопасны к повторному получению.
type SendArgs struct {
	V    int       `json:"v"`
	Mail string    `json:"mail"`
	Ref  uuid.UUID `json:"ref"`
}

func (SendArgs) Kind() string { return "mail.send" }

func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Mail, MaxAttempts: 10}
}

type SendWorker struct {
	river.WorkerDefaults[SendArgs]
	reg    *Registry
	sender Sender
}

func NewSendWorker(r *Registry, s Sender) *SendWorker { return &SendWorker{reg: r, sender: s} }

func (*SendWorker) Timeout(*river.Job[SendArgs]) time.Duration { return time.Minute }

func (w *SendWorker) Work(ctx context.Context, job *river.Job[SendArgs]) error {
	if job.Args.V != 1 {
		// аргументы более нового релиза: повтор возьмёт воркер, который их понимает
		return fmt.Errorf("mail: версия аргументов %d не поддерживается этим воркером", job.Args.V)
	}
	c, ok := w.reg.m[job.Args.Mail]
	if !ok {
		// воркер прежнего релиза при перекрытии выкатки: повтор возьмёт воркер нового
		return fmt.Errorf("mail: вид %q не зарегистрирован в этом воркере", job.Args.Mail)
	}
	m, err := c.Compose(ctx, job.Args.Ref)
	if errors.Is(err, ErrSkip) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	return w.sender.Send(ctx, m)
}

func AddWorkers(w *river.Workers, r *Registry, s Sender) {
	river.AddWorker(w, NewSendWorker(r, s))
}
