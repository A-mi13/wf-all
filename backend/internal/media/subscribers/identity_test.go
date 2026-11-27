package subscribers_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/identity"
	"wf/backend/internal/media"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/media/subscribers"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

var at = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func seed(t *testing.T, p dbtest.Pools, st media.State, by media.Uploader) uuid.UUID {
	t.Helper()
	owner := media.OwnerRef{Type: mediatest.OwnerType, ID: uuid.Must(uuid.NewV7())}
	s := mediatest.Seed{Purpose: mediatest.PurposeImage, Visibility: media.VisibilityPublic, State: st, By: by, At: at,
		ExpiresAt: at.Add(15 * time.Minute)}
	if st == media.StatePendingUpload {
		s.Intended = owner
	} else {
		s.Owner = owner
	}
	oid, err := mediatest.SeedObject(context.Background(), p.Owner, s)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func count(t *testing.T, p dbtest.Pools, q string, args ...any) int {
	t.Helper()
	var n int
	if err := p.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// I-6, §3.10: identity.user_deleted доходит до подписчика media через relay и River под ролью worker (как
// events/e2e_test.go): незавершённые билеты аккаунта удалены, у остальных его объектов uploaded_by_user обнулён (CHECK
// «не больше одного загрузившего» допускает оба NULL); чужие объекты не тронуты; второе событие того же аккаунта —
// без ошибки.
func TestSubscriberUserDeleted(t *testing.T) {
	t.Parallel()
	p := dbtest.NewPoolsAs(t, "worker")
	ctx := context.Background()
	user, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	mine := media.Uploader{UserID: user}
	pending := []uuid.UUID{seed(t, p, media.StatePendingUpload, mine), seed(t, p, media.StatePendingUpload, mine)}
	kept := []uuid.UUID{seed(t, p, media.StateUploaded, mine), seed(t, p, media.StateReady, mine),
		seed(t, p, media.StateDetached, mine), seed(t, p, media.StateRejected, mine)}
	othersPending := seed(t, p, media.StatePendingUpload, media.Uploader{UserID: other})
	othersReady := seed(t, p, media.StateReady, media.Uploader{UserID: other})
	staffs := seed(t, p, media.StateReady, media.Uploader{StaffID: uuid.Must(uuid.NewV7())})

	log := mediatest.Discard()
	reg, err := events.NewRegistry(subscribers.Subscriptions(subscribers.Deps{Log: log})...)
	if err != nil {
		t.Fatal(err)
	}
	w := river.NewWorkers()
	events.AddWorkers(w, p.As, reg)
	client, err := river.NewClient(riverpgxv5.New(p.As), &river.Config{
		Queues: map[string]river.QueueConfig{events.QueueEvents: {MaxWorkers: 2}}, Workers: w, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	completed, stopC := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer stopC()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Stop(sctx)
	})
	rctx, cancelRelay := context.WithCancel(ctx)
	relayDone := make(chan error, 1)
	relay := events.NewRelay(p.As, client, reg, log, events.RelayConfig{Batch: 10, Poll: time.Hour})
	go func() { relayDone <- relay.Run(rctx) }()
	t.Cleanup(func() {
		cancelRelay()
		if err := <-relayDone; err != nil {
			t.Error(err)
		}
	})

	deliver := func(version int64) {
		t.Helper()
		if err := db.InTx(ctx, p.Owner, func(ctx context.Context, _ pgx.Tx) error {
			_, err := events.Publish(ctx, identity.UserDeleted(identity.UserRef{ID: user, Version: version}))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-completed:
			if e.Kind != river.EventKindJobCompleted || e.Job.Kind != "events.deliver" {
				t.Fatalf("доставка: %s %s %v", e.Kind, e.Job.Kind, e.Job.Errors)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("доставка не завершилась за 30 с")
		}
	}
	check := func() {
		t.Helper()
		for _, oid := range pending {
			if n := count(t, p, `SELECT count(*) FROM media_objects WHERE id = $1`, oid); n != 0 {
				t.Fatalf("незавершённый билет %s не удалён", oid)
			}
		}
		for _, oid := range kept {
			if n := count(t, p, `SELECT count(*) FROM media_objects WHERE id = $1 AND uploaded_by_user IS NULL
				AND uploaded_by_staff IS NULL`, oid); n != 1 {
				t.Fatalf("объект %s: загрузивший не обнулён или строки нет", oid)
			}
		}
		if n := count(t, p, `SELECT count(*) FROM media_objects WHERE uploaded_by_user = $1`, user); n != 0 {
			t.Fatalf("объектов аккаунта с загрузившим: %d", n)
		}
		if n := count(t, p, `SELECT count(*) FROM media_objects WHERE (id = $1 OR id = $2) AND uploaded_by_user = $3`,
			othersPending, othersReady, other); n != 2 {
			t.Fatalf("чужие объекты тронуты: %d", n)
		}
		if n := count(t, p, `SELECT count(*) FROM media_objects WHERE id = $1 AND uploaded_by_staff IS NOT NULL`, staffs); n != 1 {
			t.Fatal("объект сотрудника тронут")
		}
	}
	deliver(5)
	check()
	deliver(6) // второе событие (другой id) — обработчик выполняется снова, без ошибки
	check()
}

// Подписка: <модуль>.<имя>, identity.user_deleted, каждое событие.
func TestSubscriptions(t *testing.T) {
	t.Parallel()
	subs := subscribers.Subscriptions(subscribers.Deps{Log: mediatest.Discard()})
	if len(subs) != 1 || subs[0].EventType != identity.EventUserDeleted || subs[0].Delivery != events.EveryEvent ||
		subs[0].Subscriber != "media.uploaders" || subs[0].Handle == nil {
		t.Fatalf("подписки: %+v", subs)
	}
}
