package main

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

	"wf/backend/internal/identity"
	identityjobs "wf/backend/internal/identity/jobs"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/periodic"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/locales"
)

// Непредвиденное 1 финального ревью: исполняющий клиент воркера — от queue.MaintenanceConfig, как у admin-api
// (TestAdminRiverMaintenance): лидером схемы может стать любой, обслуживание River не должно зависеть от того, кто.
func TestWorkerRiverMaintenance(t *testing.T) {
	t.Parallel()
	workers := river.NewWorkers()
	c := riverConfig(config.Queues{Events: 3, Lifecycle: 1, Notify: 1, Mail: 1, Stats: 1, Media: 1, Maintenance: 1},
		workers, slog.New(slog.DiscardHandler))
	if d := queue.MaintenanceDiff(c); len(d) != 0 {
		t.Fatalf("настройки River воркера мимо queue.MaintenanceConfig: %v", d)
	}
	if c.Workers != workers || c.Queues[queue.Events].MaxWorkers != 3 ||
		len(c.PeriodicJobs) != len(periodic.Jobs())+len(identityjobs.Periodic()) {
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
// равны общему: платформа + identity. Зеркало — TestPeriodicSameInBothBinaries в cmd/admin-api (оба сверяются с
// одним эталоном, значит, и между собой).
func TestPeriodicSameInBothBinaries(t *testing.T) {
	t.Parallel()
	c := riverConfig(config.Queues{Maintenance: 1}, river.NewWorkers(), slog.New(slog.DiscardHandler))
	want := periodicIDs(append(periodic.Jobs(), identityjobs.Periodic()...))
	if got := periodicIDs(c.PeriodicJobs); !slices.Equal(got, want) || len(want) != 7 {
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
