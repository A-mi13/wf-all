package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
)

// Виды задач обоих исполняющих бинарников (I1 ревью Task 10c). Лидер River — любой клиент схемы, и его
// рескьюер (задача running дольше часа — процесс убит посреди неё) ищет исполнителя вида в СВОЁМ реестре: нет —
// задача выбрасывается (discarded), даже если её исполняет другой бинарник. Поэтому воркер и admin-api
// регистрируют «чужие» виды заглушками (AddForeign): рескьюер переводит зависшую задачу в retryable, а исполнит
// её свой бинарник — чужие очереди заглушку не вызывают. Имена и сроки — здесь, в платформе: воркер не
// импортирует staff (archtest), admin-api — исполнителей воркера. Новый вид — строка в своём списке со сроком
// его исполнителя (Timeout); сверка списков с реестрами — тесты cmd/worker и cmd/admin-api.

// Kind — вид задачи, который исполняет один из бинарников, и срок его исполнителя (Timeout; 0 — срок клиента).
// Срок нужен заглушке: рескьюер не трогает задачу, пока не вышел срок исполнителя.
type Kind struct {
	Name    string
	Timeout time.Duration
}

// WorkerKinds — виды, которые исполняет воркер (cmd/worker). Новый срез на вызов.
func WorkerKinds() []Kind {
	return []Kind{
		{Name: "platform.ping"},
		{Name: "events.deliver", Timeout: time.Minute},
		{Name: "events.cleanup", Timeout: 10 * time.Minute},
		{Name: "ratelimit.cleanup", Timeout: 5 * time.Minute},
		{Name: "humancheck.cleanup", Timeout: 5 * time.Minute},
		{Name: "idempotency.cleanup", Timeout: 10 * time.Minute},
		{Name: "geo.import", Timeout: 30 * time.Minute},
		{Name: "mail.send", Timeout: time.Minute},
	}
}

// AdminKinds — виды, которые исполняет admin-api (очередь staff). Новый срез на вызов.
func AdminKinds() []Kind {
	return []Kind{{Name: "staff.mail", Timeout: time.Minute}}
}

// AddForeign — заглушки видов kinds, которые исполняет другой бинарник: только чтобы рескьюер этого клиента,
// став лидером, вернул зависшую задачу в retryable, а не выбросил её.
func AddForeign(w *river.Workers, kinds []Kind) {
	for _, k := range kinds {
		river.AddWorkerArgs(w, foreignArgs{kind: k.Name}, &foreignWorker{timeout: k.Timeout})
	}
}

// foreignArgs — аргументы чужого вида: вид задаётся значением (River.AddWorkerArgs), содержимое не читается —
// любой JSON-объект разбирается.
type foreignArgs struct{ kind string }

func (a foreignArgs) Kind() string { return a.kind }

// foreignWorker — заглушка чужого вида: срок — как у настоящего исполнителя; исполнить отказывается ошибкой
// (обычный повтор). Вызваться может, только если чужую очередь по ошибке подключили этому бинарнику.
type foreignWorker struct {
	river.WorkerDefaults[foreignArgs]
	timeout time.Duration
}

func (w *foreignWorker) Timeout(*river.Job[foreignArgs]) time.Duration { return w.timeout }

func (*foreignWorker) Work(_ context.Context, job *river.Job[foreignArgs]) error {
	return fmt.Errorf("queue: вид %q исполняет другой бинарник — очередь %q подключена не тому клиенту", job.Kind, job.Queue)
}
