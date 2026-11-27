package workerhost

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/cmd/internal/mediapurposes"
	geojobs "wf/backend/internal/geo/jobs"
	"wf/backend/internal/identity"
	identityjobs "wf/backend/internal/identity/jobs"
	mediajobs "wf/backend/internal/media/jobs"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/rivertest"
	"wf/backend/locales"
)

func names(lists ...[]queue.Kind) []string {
	var out []string
	for _, l := range lists {
		for _, k := range l {
			out = append(out, k.Name)
		}
	}
	slices.Sort(out)
	return out
}

// Реестр воркера (I1 ревью 10c): настоящие исполнители — ровно queue.WorkerKinds (новый вид без строки там —
// падение здесь, а без строки admin-api не узнает его и выбросит зависшую задачу), сроки списка — сроки
// настоящих исполнителей; исполняющий клиент знает и виды admin-api (заглушки) — объединение обоих списков.
func TestWorkerKinds(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	reg, err := Subscriptions(log)
	if err != nil {
		t.Fatal(err)
	}
	idd := lazyIdentityDeps(t)
	md := lazyMediaDeps(t, idd)
	own, err := realWorkers(nil, reg, geojobs.Config{}, log, nil, idd, md)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rivertest.Kinds(own), names(queue.WorkerKinds()); !slices.Equal(got, want) {
		t.Fatalf("исполнители воркера %v, queue.WorkerKinds %v", got, want)
	}
	timeouts := map[string]time.Duration{
		"platform.ping":              (&queue.PingWorker{}).Timeout(nil),
		"events.deliver":             (&events.DeliverWorker{}).Timeout(nil),
		"events.cleanup":             (&events.CleanupWorker{}).Timeout(nil),
		"ratelimit.cleanup":          (&ratelimit.CleanupWorker{}).Timeout(nil),
		"humancheck.cleanup":         (&humancheck.CleanupWorker{}).Timeout(nil),
		"idempotency.cleanup":        (&idempotency.CleanupWorker{}).Timeout(nil),
		"geo.import":                 (&geojobs.ImportWorker{}).Timeout(nil),
		"mail.send":                  (&mail.SendWorker{}).Timeout(nil),
		"identity.maintenance":       (&identityjobs.MaintenanceWorker{}).Timeout(nil),
		"identity.anonymize":         (&identityjobs.AnonymizeWorker{}).Timeout(nil),
		"identity.mfa_reset_execute": (&identityjobs.MfaResetWorker{}).Timeout(nil),
		"media.process":              (&mediajobs.ProcessWorker{}).Timeout(nil),
		"media.purge":                (&mediajobs.PurgeWorker{}).Timeout(nil),
		"media.cleanup":              (&mediajobs.CleanupWorker{}).Timeout(nil),
	}
	for _, k := range queue.WorkerKinds() {
		d, ok := timeouts[k.Name]
		if !ok || d != k.Timeout {
			t.Errorf("%s: срок в queue.WorkerKinds %s, у исполнителя %s (есть в таблице теста: %v)", k.Name, k.Timeout, d, ok)
		}
	}
	all, err := allWorkers(nil, reg, geojobs.Config{}, log, nil, idd, md)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rivertest.Kinds(all), names(queue.WorkerKinds(), queue.AdminKinds()); !slices.Equal(got, want) {
		t.Fatalf("исполняющий клиент воркера знает %v, ждали %v", got, want)
	}
}

// defaultWorker — конфиг воркера по умолчанию (WORKER_QUEUE_* и флаг импорта — из тегов envDefault).
func defaultWorker(t *testing.T) config.Worker {
	t.Helper()
	c, err := config.Load[config.Worker]("WORKER_", []string{"WORKER_DATABASE_URL=postgres://w@127.0.0.1:1/wf"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Очереди видов воркера (m-r2 ревью раунда 1): каждая задача из queue.WorkerKinds ставится в очередь, которую
// обслуживает воркер (queues на умолчаниях, импорт включён — R3), и не в очередь admin-api (queue.Staff) — иначе её
// взяла бы заглушка чужого бинарника. Таблица теста — по виду на строку queue.WorkerKinds (новый вид без строки —
// падение).
func TestWorkerKindsQueues(t *testing.T) {
	t.Parallel()
	// вид — из самих аргументов (Kind), а не строкой таблицы: перепутанная пара вида и аргументов не пройдёт (m-r5)
	got := queuesOf(queue.PingArgs{}, events.DeliverArgs{}, events.CleanupArgs{}, ratelimit.CleanupArgs{},
		humancheck.CleanupArgs{}, idempotency.CleanupArgs{}, geojobs.ImportArgs{}, mail.SendArgs{},
		identityjobs.MaintenanceArgs{}, identityjobs.AnonymizeArgs{}, identityjobs.MfaResetExecuteArgs{},
		withOpts{mediajobs.ProcessArgs{}, mediajobs.ProcessInsertOpts()},
		withOpts{mediajobs.PurgeArgs{}, mediajobs.PurgeInsertOpts()}, mediajobs.CleanupArgs{})
	c := defaultWorker(t)
	if !c.GeoImportEnabled {
		t.Fatal("по умолчанию импорт выключен")
	}
	served := queues(c)
	for _, k := range queue.WorkerKinds() {
		q, ok := got[k.Name]
		if !ok {
			t.Errorf("%s: нет строки в таблице очередей теста", k.Name)
			continue
		}
		if _, ok := served[q]; !ok || q == queue.Staff {
			t.Errorf("%s ставится в очередь %q — её не обслуживает воркер", k.Name, q)
		}
	}
	if got["geo.import"] != queue.Import {
		t.Errorf("geo.import в очереди %q, ждали %q", got["geo.import"], queue.Import)
	}
}

// Флаг импорта (спека §8.2, R3): выключен — очереди import нет в карте River (а не MaxWorkers 0: River отверг бы
// его в NewClient после подключения), остальные семь на месте; включён — import с конкурентностью из конфига.
func TestQueuesImportFlag(t *testing.T) {
	t.Parallel()
	c := defaultWorker(t)
	c.Queues.Import = 3
	on := queues(c)
	if len(on) != 8 || on[queue.Import].MaxWorkers != 3 {
		t.Fatalf("импорт включён: %v", on)
	}
	c.GeoImportEnabled = false
	off := queues(c)
	if _, ok := off[queue.Import]; ok || len(off) != 7 {
		t.Fatalf("импорт выключен, а очереди: %v", off)
	}
	for name, qc := range on {
		if name != queue.Import && off[name] != qc {
			t.Errorf("%s: при выключенном импорте %v, при включённом %v", name, off[name], qc)
		}
	}
}

// argsWithQueue — аргументы задачи со своей очередью (InsertOpts).
type argsWithQueue interface {
	river.JobArgs
	river.JobArgsWithInsertOpts
}

// withOpts — аргументы, чьи опции вставки — функция пакета, а не метод (media: ProcessInsertOpts, PurgeInsertOpts —
// их передаёт intx явно); вид — из самих аргументов.
type withOpts struct {
	river.JobArgs
	opts river.InsertOpts
}

func (a withOpts) InsertOpts() river.InsertOpts { return a.opts }

// queuesOf — очередь каждого вида по аргументам: вид — args.Kind().
func queuesOf(args ...argsWithQueue) map[string]string {
	out := make(map[string]string, len(args))
	for _, a := range args {
		out[a.Kind()] = a.InsertOpts().Queue
	}
	return out
}

// Непредвиденное 1 финального ревью: исполняющий клиент воркера — от queue.MaintenanceConfig, как у admin-api
// (TestAdminRiverMaintenance): лидером схемы может стать любой, обслуживание River не должно зависеть от того, кто.
func TestWorkerRiverMaintenance(t *testing.T) {
	t.Parallel()
	workers := river.NewWorkers()
	c := riverConfig(queue.Config(config.Queues{Events: 3, Lifecycle: 1, Notify: 1, Mail: 1, Stats: 1, Media: 1,
		Maintenance: 1, Import: 1}), workers, slog.New(slog.DiscardHandler))
	if d := queue.MaintenanceDiff(c); len(d) != 0 {
		t.Fatalf("настройки River воркера мимо queue.MaintenanceConfig: %v", d)
	}
	if c.Workers != workers || c.Queues[queue.Events].MaxWorkers != 3 ||
		len(c.PeriodicJobs) != len(periodic.Jobs())+len(identityjobs.Periodic())+len(mediajobs.Periodic()) {
		t.Fatalf("поля воркера: %+v", c)
	}
}

// periodicIDs — ID периодических задач по алфавиту (River их не отдаёт; reflect только читает строку). ID = вид
// задачи у платформы и identity.
func periodicIDs(jobs []*river.PeriodicJob) []string {
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, reflect.ValueOf(j).Elem().FieldByName("opts").Elem().FieldByName("ID").String())
	}
	slices.Sort(out)
	return out
}

// R7 плана I-2: лидер River ставит периодику по своему списку, лидером бывает и admin-api — списки двух бинарников
// равны общему: платформа + identity + media (план media, Task 10). Зеркало — TestPeriodicSameInBothBinaries в cmd/admin-api (оба сверяются с
// одним эталоном, значит, и между собой).
func TestPeriodicSameInBothBinaries(t *testing.T) {
	t.Parallel()
	c := riverConfig(queue.Config(config.Queues{Maintenance: 1}), river.NewWorkers(), slog.New(slog.DiscardHandler))
	want := periodicIDs(append(append(periodic.Jobs(), identityjobs.Periodic()...), mediajobs.Periodic()...))
	if got := periodicIDs(c.PeriodicJobs); !slices.Equal(got, want) || len(want) != 8 {
		t.Fatalf("периодика воркера %v, ждали %v", got, want)
	}
}

// lazyIdentityDeps — зависимости identity без подключения к базе (пул ленивый): для сверки реестров.
func lazyIdentityDeps(t *testing.T) identity.WorkerDeps {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://w@127.0.0.1:1/wf?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	k, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ring, err := keys.NewRing([]string{k})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return identity.WorkerDeps{Pool: pool, Clock: clocktest.New(time.Now()), Secrets: ring, River: ins, Catalog: cat, Log: log}
}

// lazyMediaDeps — зависимости media без подключения к базе (пул ленивый) и с хранилищем none: для сверки реестров.
func lazyMediaDeps(t *testing.T, idd identity.WorkerDeps) mediajobs.WorkerDeps {
	t.Helper()
	reg, err := mediapurposes.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return mediajobs.WorkerDeps{Pool: idd.Pool, Clock: idd.Clock, Registry: reg, Check: mediapurposes.Check(), Log: idd.Log}
}

// Подписчики воркера: media на удаление аккаунта (спека media §3.10) — в реестре relay и переигровки.
func TestWorkerSubscriptions(t *testing.T) {
	t.Parallel()
	reg, err := Subscriptions(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("media.uploaders", "identity.user_deleted"); !ok {
		t.Fatal("подписки media.uploaders на identity.user_deleted нет в реестре воркера")
	}
}

// Письма identity — в реестре воркера (mails): без них задача mail.send вида identity.* падала бы «неизвестный вид».
func TestWorkerMailsIdentity(t *testing.T) {
	t.Parallel()
	r, err := mails(lazyIdentityDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	m := reflect.ValueOf(r).Elem().FieldByName("m")
	var kinds []string
	for _, k := range m.MapKeys() {
		kinds = append(kinds, k.String())
	}
	for _, want := range []string{"identity.signup_code", "identity.account_deleted", "identity.mfa_reset_done"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("письма %s нет в реестре воркера: %v", want, kinds)
		}
	}
}
