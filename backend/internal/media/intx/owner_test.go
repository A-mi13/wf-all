package intx_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/media"
	"wf/backend/internal/media/intx"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/db"
)

// I-2: ручка-образец «заменить» (§3.6): Stage до транзакции, затем одна db.InTx с TakeDownInTx старого и
// CreateDirectInTx нового — ровно одна закоммиченная транзакция (хук идемпотентности считает коммиты).
func TestIdempotentHandlerOneTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "admin")
	svc, _ := stageService(t, e)
	a := newOwner()
	staff := media.Uploader{StaffID: uuid.Must(uuid.NewV7())}
	old := e.seed(t, mediatest.Seed{State: media.StateReady, Owner: a, By: player(), Variants: []string{"full"}})
	var mu sync.Mutex
	committed, rolled := 0, 0
	ctx := db.WithTxHook(context.Background(), func(context.Context, pgx.Tx) (func(bool), error) {
		return func(ok bool) {
			mu.Lock()
			defer mu.Unlock()
			if ok {
				committed++
			} else {
				rolled++
			}
		}, nil
	})
	st, err := svc.Stage(ctx, mediatest.PurposeImage, strings.NewReader("png"), media.Declared{ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	actor := media.Actor{StaffID: staff.StaffID, Role: "moderator", Label: "Модератор"}
	var img media.Image
	err = db.InTx(ctx, e.pools.As, func(ctx context.Context, tx pgx.Tx) error {
		if err := e.x.TakeDownInTx(ctx, tx, old, actor, "moderation.replaced"); err != nil {
			return err
		}
		var err error
		img, err = e.x.CreateDirectInTx(ctx, tx, st, a, staff)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed != 1 || rolled != 0 {
		t.Fatalf("коммитов %d, откатов %d", committed, rolled)
	}
	if img.ID != st.ObjectID || img.State != media.StateProcessing {
		t.Fatalf("Image: %+v", img)
	}
	if r, _ := object(t, e.pools.Owner, old); r.State != "taken_down" {
		t.Fatalf("старый: %+v", r)
	}
}

// CreateDirectInTx (§2.3): объект сразу uploaded с владельцем, загрузивший — by, заявленное — из Staged, задача
// media.process в той же tx. Тип владельца не из цели, неизвестная цель, Staged сверх предела или чужого типа, два
// загрузивших — ошибка программиста без записи.
func TestCreateDirectInTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "admin")
	a := newOwner()
	staff := media.Uploader{StaffID: uuid.Must(uuid.NewV7())}
	st := media.Staged{ObjectID: uuid.Must(uuid.NewV7()), Purpose: mediatest.PurposeImagePrivate, Bytes: 42, ContentType: "image/webp"}
	run := func(s media.Staged, o media.OwnerRef, by media.Uploader) (media.Image, error) {
		var img media.Image
		err := e.inTx(func(ctx context.Context, tx pgx.Tx) error {
			var err error
			img, err = e.x.CreateDirectInTx(ctx, tx, s, o, by)
			return err
		})
		return img, err
	}
	with := func(f func(*media.Staged)) media.Staged {
		s := st
		f(&s)
		return s
	}
	bad := map[string]struct {
		s  media.Staged
		o  media.OwnerRef
		by media.Uploader
	}{
		"тип владельца":   {st, media.OwnerRef{Type: "other", ID: a.ID}, staff},
		"цель":            {with(func(s *media.Staged) { s.Purpose = "nope" }), a, staff},
		"сверх предела":   {with(func(s *media.Staged) { s.Bytes = mediatest.MaxBytes + 1 }), a, staff},
		"пусто":           {with(func(s *media.Staged) { s.Bytes = 0 }), a, staff},
		"тип файла":       {with(func(s *media.Staged) { s.ContentType = "text/html" }), a, staff},
		"без id":          {with(func(s *media.Staged) { s.ObjectID = uuid.Nil }), a, staff},
		"два загрузивших": {st, a, media.Uploader{UserID: uuid.New(), StaffID: uuid.New()}},
	}
	for name, c := range bad {
		if _, err := run(c.s, c.o, c.by); err == nil || errors.Is(err, media.ErrObjectUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_objects`); n != 0 {
		t.Fatalf("строк после отказов: %d", n)
	}
	img, err := run(st, a, staff)
	if err != nil || img.ID != st.ObjectID || img.State != media.StateProcessing {
		t.Fatalf("CreateDirectInTx: %+v %v", img, err)
	}
	var state, vis, ot, dt string
	var oid, sb uuid.UUID
	var ub *uuid.UUID
	var size int64
	var uploadedAt time.Time
	if err := e.pools.Owner.QueryRow(context.Background(), `SELECT state, visibility, owner_type, owner_id,
		uploaded_by_staff, uploaded_by_user, declared_type, declared_bytes, uploaded_at FROM media_objects WHERE id = $1`,
		st.ObjectID).Scan(&state, &vis, &ot, &oid, &sb, &ub, &dt, &size, &uploadedAt); err != nil {
		t.Fatal(err)
	}
	if state != "uploaded" || vis != "private" || ot != a.Type || oid != a.ID || sb != staff.StaffID || ub != nil ||
		dt != "image/webp" || size != 42 || !uploadedAt.Equal(at) {
		t.Fatalf("строка: %s %s %s %s %s %v %s %d %v", state, vis, ot, oid, sb, ub, dt, size, uploadedAt)
	}
	if n := processJobs(t, e.pools.Owner, st.ObjectID); n != 1 {
		t.Fatalf("задач: %d", n)
	}
	// система (§3.7): оба загрузивших пусты
	sys := media.Staged{ObjectID: uuid.Must(uuid.NewV7()), Purpose: mediatest.PurposeImage, Bytes: 1, ContentType: "image/png"}
	if _, err := run(sys, a, media.Uploader{}); err != nil {
		t.Fatalf("система: %v", err)
	}
}

// m3-1: открепление конечного (rejected, detached, taken_down) и отсутствующей строки — nil без изменений и задач.
func TestDetachFinalIsNoop(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a := newOwner()
	for _, s := range []media.State{media.StateRejected, media.StateDetached, media.StateTakenDown} {
		id := e.seed(t, mediatest.Seed{State: s, Owner: a, By: player(), Variants: []string{"full"}})
		if err := e.inTx(func(ctx context.Context, tx pgx.Tx) error { return e.x.DetachInTx(ctx, tx, id, a) }); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if r, _ := object(t, e.pools.Owner, id); r.State != string(s) || r.Version != 1 {
			t.Fatalf("%s: %+v", s, r)
		}
	}
	if err := e.inTx(func(ctx context.Context, tx pgx.Tx) error { return e.x.DetachInTx(ctx, tx, uuid.New(), a) }); err != nil {
		t.Fatalf("нет строки: %v", err)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM river_job`); n != 0 {
		t.Fatalf("задач: %d", n)
	}
}

// Чужой владелец (в том числе у конечного) и незавершённый (владельца ещё нет) — object_unavailable без изменений.
func TestDetachForeignOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, b, by := newOwner(), newOwner(), player()
	ready := e.seed(t, mediatest.Seed{State: media.StateReady, Owner: a, By: by, Variants: []string{"full"}})
	taken := e.seed(t, mediatest.Seed{State: media.StateTakenDown, Owner: a, By: by})
	pending := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: b, By: by, ExpiresAt: at.Add(time.Minute)})
	for name, id := range map[string]uuid.UUID{"ready": ready, "конечный": taken, "pending_upload": pending} {
		if err := e.inTx(func(ctx context.Context, tx pgx.Tx) error { return e.x.DetachInTx(ctx, tx, id, b) }); !errors.Is(err, media.ErrObjectUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if r, _ := object(t, e.pools.Owner, ready); r.State != "ready" || r.Version != 1 {
		t.Fatalf("строка: %+v", r)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM river_job`); n != 0 {
		t.Fatalf("задач: %d", n)
	}
}

// §2.3: открепление uploaded|processing|ready — detached, ended_at, version + 1; media.purge в той же tx: ключи
// вариантов в бакете видимости и входящее.
func TestDetachPurges(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a := newOwner()
	pub := e.seed(t, mediatest.Seed{State: media.StateReady, Owner: a, By: player(), Variants: []string{"full", "thumb"}})
	priv := e.seed(t, mediatest.Seed{Purpose: mediatest.PurposeImagePrivate, Visibility: media.VisibilityPrivate,
		State: media.StateReady, Owner: a, By: player(), Variants: []string{"full"}})
	proc := e.seed(t, mediatest.Seed{State: media.StateProcessing, Owner: a, By: player()})
	e.clk.Advance(time.Minute)
	for _, id := range []uuid.UUID{pub, priv, proc} {
		if err := e.inTx(func(ctx context.Context, tx pgx.Tx) error { return e.x.DetachInTx(ctx, tx, id, a) }); err != nil {
			t.Fatal(err)
		}
		r, _ := object(t, e.pools.Owner, id)
		if r.State != "detached" || r.Version != 2 || r.EndedAt == nil || !r.EndedAt.Equal(at.Add(time.Minute)) {
			t.Fatalf("строка: %+v", r)
		}
	}
	p := purges(t, e.pools.Owner, pub)
	if len(p) != 2 || !slices.Equal(p[jobs.BucketPub].Keys, []string{mediatest.VariantKey(pub, "full"), mediatest.VariantKey(pub, "thumb")}) ||
		!slices.Equal(p[jobs.BucketIn].Keys, []string{pub.String()}) {
		t.Fatalf("purge публичного: %+v", p)
	}
	p = purges(t, e.pools.Owner, priv)
	if len(p) != 2 || !slices.Equal(p[jobs.BucketPriv].Keys, []string{mediatest.VariantKey(priv, "full")}) {
		t.Fatalf("purge закрытого: %+v", p)
	}
	// строк вариантов нет, но неудачная попытка обработки могла записать файлы (ревью Task 9, I-1): все ключи цели,
	// без адресов (открепление)
	p = purges(t, e.pools.Owner, proc)
	if len(p) != 2 || !slices.Equal(p[jobs.BucketIn].Keys, []string{proc.String()}) ||
		!slices.Equal(p[jobs.BucketPub].Keys, []string{mediatest.VariantKey(proc, "thumb"), mediatest.VariantKey(proc, "full")}) ||
		len(p[jobs.BucketPub].PublicURLs) != 0 {
		t.Fatalf("purge без строк вариантов: %+v", p)
	}
}

// §2.3, §3.8: снятие — taken_down, аудит media.object_taken_down (актор, объект, код причины), событие с версией из
// RETURNING, media.purge с публичными адресами вариантов — всё в одной транзакции (её откат — ничего); из конечного
// — nil без записей; незавершённый — object_unavailable; нет строки — nil.
func TestTakeDownWritesAuditEventPurge(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, author := newOwner(), player()
	actor := media.Actor{UserID: author.UserID, Role: "player"}
	id := e.seed(t, mediatest.Seed{State: media.StateReady, Owner: a, By: author, Variants: []string{"full", "thumb"}})
	ctx := context.Background()
	takeDown := func(objectID uuid.UUID) error {
		return e.inTx(func(ctx context.Context, tx pgx.Tx) error {
			return e.x.TakeDownInTx(ctx, tx, objectID, actor, "author_removed")
		})
	}
	records := func() int {
		return count(t, e.pools.Owner, `SELECT count(*) FROM audit_log`) + count(t, e.pools.Owner, `SELECT count(*) FROM outbox`) +
			count(t, e.pools.Owner, `SELECT count(*) FROM river_job`)
	}

	errRollback := errors.New("откат")
	if err := e.inTx(func(ctx context.Context, tx pgx.Tx) error {
		if err := e.x.TakeDownInTx(ctx, tx, id, actor, "author_removed"); err != nil {
			return err
		}
		return errRollback
	}); !errors.Is(err, errRollback) {
		t.Fatalf("откат: %v", err)
	}
	if r, _ := object(t, e.pools.Owner, id); r.State != "ready" || records() != 0 {
		t.Fatalf("после отката: %+v, записей %d", r, records())
	}

	if err := takeDown(id); err != nil {
		t.Fatal(err)
	}
	r, _ := object(t, e.pools.Owner, id)
	if r.State != "taken_down" || r.Version != 2 || r.EndedAt == nil {
		t.Fatalf("строка: %+v", r)
	}
	var action, objType, reason, role string
	var objID, actorUser uuid.UUID
	if err := e.pools.Owner.QueryRow(ctx, `SELECT action, object_type, object_id, reason, actor_role, actor_user_id
		FROM audit_log`).Scan(&action, &objType, &objID, &reason, &role, &actorUser); err != nil {
		t.Fatal(err)
	}
	if action != "media.object_taken_down" || objType != "media_object" || objID != id || reason != "author_removed" ||
		role != "player" || actorUser != author.UserID {
		t.Fatalf("аудит: %s %s %s %s %s %s", action, objType, objID, reason, role, actorUser)
	}
	var evType, aggType string
	var aggID uuid.UUID
	var aggVer int64
	var payload map[string]any
	if err := e.pools.Owner.QueryRow(ctx, `SELECT event_type, aggregate_type, aggregate_id, aggregate_version, payload
		FROM outbox`).Scan(&evType, &aggType, &aggID, &aggVer, &payload); err != nil {
		t.Fatal(err)
	}
	if evType != media.EventObjectTakenDown || aggType != media.AggregateObject || aggID != id || aggVer != 2 ||
		payload["owner_id"] != a.ID.String() || payload["owner_type"] != a.Type || payload["purpose"] != mediatest.PurposeImage {
		t.Fatalf("событие: %s %s %s %d %v", evType, aggType, aggID, aggVer, payload)
	}
	p := purges(t, e.pools.Owner, id)
	keys := []string{mediatest.VariantKey(id, "full"), mediatest.VariantKey(id, "thumb")}
	urls := []string{mediatest.PublicBaseURL + keys[0], mediatest.PublicBaseURL + keys[1]}
	if len(p) != 2 || !slices.Equal(p[jobs.BucketPub].Keys, keys) || !slices.Equal(p[jobs.BucketPub].PublicURLs, urls) ||
		p[jobs.BucketPub].V != 1 || !slices.Equal(p[jobs.BucketIn].Keys, []string{id.String()}) || len(p[jobs.BucketIn].PublicURLs) != 0 {
		t.Fatalf("purge: %+v", p)
	}

	before := records()
	if err := takeDown(id); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if got := records(); got != before {
		t.Fatalf("записей после повтора: %d, было %d", got, before)
	}
	pending := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: author, ExpiresAt: at.Add(time.Minute)})
	if err := takeDown(pending); !errors.Is(err, media.ErrObjectUnavailable) {
		t.Fatalf("pending_upload: %v", err)
	}
	if err := takeDown(uuid.New()); err != nil {
		t.Fatalf("нет строки: %v", err)
	}
	if err := e.inTx(func(ctx context.Context, tx pgx.Tx) error {
		return e.x.TakeDownInTx(ctx, tx, id, actor, "Свободный текст")
	}); err == nil {
		t.Fatal("причина — не код")
	}
}

// tx параметра — не транзакция db.InTx из ctx (ctx без транзакции, tx другой db.InTx): ErrTxMismatch до записей.
func TestTxMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, by := newOwner(), player()
	pending := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: by, ExpiresAt: at.Add(time.Minute)})
	ready := e.seed(t, mediatest.Seed{State: media.StateReady, Owner: a, By: by})
	st := media.Staged{ObjectID: uuid.Must(uuid.NewV7()), Purpose: mediatest.PurposeImage, Bytes: 1, ContentType: "image/png"}
	calls := func(ctx context.Context, tx pgx.Tx) map[string]error {
		_, attach := e.x.AttachInTx(ctx, tx, pending, a, by)
		_, direct := e.x.CreateDirectInTx(ctx, tx, st, a, by)
		return map[string]error{
			"Attach":   attach,
			"Direct":   direct,
			"Detach":   e.x.DetachInTx(ctx, tx, ready, a),
			"TakeDown": e.x.TakeDownInTx(ctx, tx, ready, media.Actor{UserID: by.UserID, Role: "player"}, "author_removed"),
		}
	}
	check := func(where string, errs map[string]error) {
		for name, err := range errs {
			if !errors.Is(err, intx.ErrTxMismatch) {
				t.Errorf("%s %s: %v", where, name, err)
			}
		}
	}
	_ = e.inTx(func(_ context.Context, tx pgx.Tx) error {
		check("ctx без транзакции", calls(context.Background(), tx))
		return nil
	})
	_ = e.inTx(func(outer context.Context, _ pgx.Tx) error {
		return e.inTx(func(_ context.Context, inner pgx.Tx) error {
			check("чужая tx", calls(outer, inner))
			return nil
		})
	})
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_objects WHERE state IN ('pending_upload', 'ready')`); n != 2 {
		t.Fatalf("строк: %d", n)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_objects`); n != 2 {
		t.Fatalf("строк всего: %d", n)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM river_job`); n != 0 {
		t.Fatalf("задач: %d", n)
	}
}
