package intx_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/media"
	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/media/intx"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

var at = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

type env struct {
	x     *intx.Tx
	pools dbtest.Pools
	clk   *clocktest.Fake
}

// newEnv — intx под ролью role (api — игрок, admin — сотрудник) с клиентом River только для вставки.
func newEnv(t *testing.T, role string) env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, role)
	clk := clocktest.New(at)
	ins, err := river.NewClient(riverpgxv5.New(pools.As), queue.InsertConfig(mediatest.Discard()))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := mediatest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	x, err := intx.New(intx.Deps{Clock: clk, Registry: reg, River: ins, PublicBaseURL: mediatest.PublicBaseURL,
		Log: mediatest.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	return env{x: x, pools: pools, clk: clk}
}

func (e env) seed(t *testing.T, s mediatest.Seed) uuid.UUID {
	t.Helper()
	if s.Purpose == "" {
		s.Purpose, s.Visibility = mediatest.PurposeImage, media.VisibilityPublic
	}
	if s.At.IsZero() {
		s.At = at
	}
	id, err := mediatest.SeedObject(context.Background(), e.pools.Owner, s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// inTx — транзакция db.InTx пула роли (как у ручки владельца).
func (e env) inTx(fn func(ctx context.Context, tx pgx.Tx) error) error {
	return db.InTx(context.Background(), e.pools.As, fn)
}

func newOwner() media.OwnerRef {
	return media.OwnerRef{Type: mediatest.OwnerType, ID: uuid.Must(uuid.NewV7())}
}

func player() media.Uploader { return media.Uploader{UserID: uuid.Must(uuid.NewV7())} }

func count(t *testing.T, p *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type objRow struct {
	State     string
	OwnerType *string
	OwnerID   *uuid.UUID
	Version   int64
	EndedAt   *time.Time
}

func object(t *testing.T, p *pgxpool.Pool, id uuid.UUID) (objRow, bool) {
	t.Helper()
	var r objRow
	err := p.QueryRow(context.Background(), `SELECT state, owner_type, owner_id, version, ended_at FROM media_objects
		WHERE id = $1`, id).Scan(&r.State, &r.OwnerType, &r.OwnerID, &r.Version, &r.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

// purges — задачи media.purge по объекту: бакет → аргументы.
func purges(t *testing.T, p *pgxpool.Pool, objectID uuid.UUID) map[string]jobs.PurgeArgs {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT args FROM river_job WHERE kind = 'media.purge'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]jobs.PurgeArgs{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var a jobs.PurgeArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(a.Keys, func(k string) bool { return strings.HasPrefix(k, objectID.String()) }) {
			if _, dup := out[a.Bucket]; dup {
				t.Fatalf("две задачи purge бакета %s", a.Bucket)
			}
			out[a.Bucket] = a
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func processJobs(t *testing.T, p *pgxpool.Pool, objectID uuid.UUID) int {
	t.Helper()
	return count(t, p, `SELECT count(*) FROM river_job WHERE kind = 'media.process' AND queue = 'media'
		AND max_attempts = 10 AND (args->>'object_id')::uuid = $1 AND (args->>'v')::int = 1`, objectID)
}

func TestNewRequiresDeps(t *testing.T) {
	t.Parallel()
	if _, err := intx.New(intx.Deps{}); err == nil {
		t.Fatal("New без зависимостей")
	}
}

// I-5 (§2.3): билет на A не прикрепляется к B; чужой загрузивший; тип владельца не из цели; окно прошло; нет строки —
// отказ без изменений. Успех — uploaded у A, билет удалён, одна задача media.process; повтор к тому же — тот же Image
// без второй задачи; повтор к другому — отказ.
func TestAttachRights(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, b, by := newOwner(), newOwner(), player()
	expires := at.Add(15 * time.Minute)
	pending := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: by, ExpiresAt: expires})
	foreignType := media.OwnerRef{Type: "other_owner", ID: uuid.Must(uuid.NewV7())}
	typed := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: foreignType, By: by, ExpiresAt: expires})
	attach := func(id uuid.UUID, o media.OwnerRef, u media.Uploader) (media.Image, error) {
		var img media.Image
		err := e.inTx(func(ctx context.Context, tx pgx.Tx) error {
			var err error
			img, err = e.x.AttachInTx(ctx, tx, id, o, u)
			return err
		})
		return img, err
	}
	for name, c := range map[string]struct {
		id uuid.UUID
		o  media.OwnerRef
		by media.Uploader
	}{
		"другой владелец":         {pending, b, by},
		"другой тип владельца":    {pending, media.OwnerRef{Type: "other_owner", ID: a.ID}, by},
		"чужой загрузивший":       {pending, a, player()},
		"сотрудник вместо игрока": {pending, a, media.Uploader{StaffID: by.UserID}},
		"тип не из цели":          {typed, foreignType, by},
		"нет строки":              {uuid.New(), a, by},
	} {
		if _, err := attach(c.id, c.o, c.by); !errors.Is(err, media.ErrObjectUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if r, _ := object(t, e.pools.Owner, pending); r.State != "pending_upload" || r.Version != 1 {
		t.Fatalf("после отказов: %+v", r)
	}
	img, err := attach(pending, a, by)
	if err != nil {
		t.Fatal(err)
	}
	if img.ID != pending || img.State != media.StateProcessing || len(img.Variants) != 0 {
		t.Fatalf("Image: %+v", img)
	}
	r, _ := object(t, e.pools.Owner, pending)
	if r.State != "uploaded" || *r.OwnerType != a.Type || *r.OwnerID != a.ID || r.Version != 2 {
		t.Fatalf("строка: %+v", r)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_uploads WHERE object_id = $1`, pending); n != 0 {
		t.Fatalf("билет остался: %d", n)
	}
	again, err := attach(pending, a, by)
	if err != nil || !equalImage(again, img) {
		t.Fatalf("повтор: %+v %v", again, err)
	}
	if n := processJobs(t, e.pools.Owner, pending); n != 1 {
		t.Fatalf("задач media.process: %d", n)
	}
	if _, err := attach(pending, b, by); !errors.Is(err, media.ErrObjectUnavailable) {
		t.Fatalf("повтор к другому: %v", err)
	}
	if _, err := attach(pending, a, player()); !errors.Is(err, media.ErrObjectUnavailable) {
		t.Fatalf("повтор чужим загрузившим: %v", err)
	}
}

func equalImage(a, b media.Image) bool {
	return a.ID == b.ID && a.State == b.State && a.RejectCode == b.RejectCode && len(a.Variants) == len(b.Variants)
}

// Окно «завершить» (m2) — строго до expires_at + 1 ч и в AttachInTx (защита в глубину после ConfirmUpload).
func TestAttachWindow(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, by := newOwner(), player()
	expires := at.Add(15 * time.Minute)
	late := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: by, ExpiresAt: expires})
	inTime := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: by, ExpiresAt: expires})
	run := func(id uuid.UUID) error {
		return e.inTx(func(ctx context.Context, tx pgx.Tx) error {
			_, err := e.x.AttachInTx(ctx, tx, id, a, by)
			return err
		})
	}
	e.clk.Set(expires.Add(time.Hour))
	if err := run(late); !errors.Is(err, media.ErrUploadExpired) {
		t.Fatalf("ровно окно: %v", err)
	}
	e.clk.Set(expires.Add(time.Hour - time.Nanosecond))
	if err := run(inTime); err != nil {
		t.Fatalf("окно − 1 нс: %v", err)
	}
}

// Два «завершить» параллельно: второй ждёт замок строки и видит прикреплённый к тому же владельцу — один переход, одна
// задача, оба успешны.
func TestAttachConcurrent(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, by := newOwner(), player()
	id := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: by, ExpiresAt: at.Add(15 * time.Minute)})
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	imgs := make([]media.Image, 2)
	for i := range errs {
		wg.Go(func() {
			errs[i] = e.inTx(func(ctx context.Context, tx pgx.Tx) error {
				<-start
				var err error
				imgs[i], err = e.x.AttachInTx(ctx, tx, id, a, by)
				return err
			})
		})
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !equalImage(imgs[0], imgs[1]) {
		t.Fatalf("исходы: %v %v %+v %+v", errs[0], errs[1], imgs[0], imgs[1])
	}
	if r, _ := object(t, e.pools.Owner, id); r.State != "uploaded" || r.Version != 2 {
		t.Fatalf("строка: %+v", r)
	}
	if n := processJobs(t, e.pools.Owner, id); n != 1 {
		t.Fatalf("задач: %d", n)
	}
}

// «Завершить» ∥ чистка окна (§3.9: ExpiredPending SKIP LOCKED + DeleteObjects в своей транзакции): ровно один исход
// на каждом объекте — прикреплён (чистка его не тронула) или удалён (прикрепление — object_unavailable).
func TestAttachVsExpiredCleanup(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, by := newOwner(), player()
	expires := at.Add(15 * time.Minute)
	// у прикрепления окно ещё живо (последняя наносекунда), чистка уже считает его прошедшим
	e.clk.Set(expires.Add(time.Hour - time.Nanosecond))
	for round := range 8 {
		id := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: by, ExpiresAt: expires})
		start := make(chan struct{})
		var wg sync.WaitGroup
		var attachErr, cleanErr error
		var deleted []uuid.UUID
		wg.Go(func() {
			attachErr = e.inTx(func(ctx context.Context, tx pgx.Tx) error {
				<-start
				_, err := e.x.AttachInTx(ctx, tx, id, a, by)
				return err
			})
		})
		wg.Go(func() {
			cleanErr = db.InTx(context.Background(), e.pools.Owner, func(ctx context.Context, tx pgx.Tx) error {
				<-start
				q := mediadb.New(tx)
				rows, err := q.ExpiredPending(ctx, mediadb.ExpiredPendingParams{Before: expires.Add(time.Nanosecond), Lim: 100})
				if err != nil || len(rows) == 0 {
					return err
				}
				ids := make([]uuid.UUID, len(rows))
				for i, r := range rows {
					ids[i] = r.ID
				}
				deleted, err = q.DeleteObjects(ctx, ids)
				return err
			})
		})
		close(start)
		wg.Wait()
		if cleanErr != nil {
			t.Fatalf("раунд %d: чистка: %v", round, cleanErr)
		}
		r, exists := object(t, e.pools.Owner, id)
		switch {
		case attachErr == nil && exists && r.State == "uploaded" && !slices.Contains(deleted, id):
		case errors.Is(attachErr, media.ErrObjectUnavailable) && !exists && slices.Contains(deleted, id):
		default:
			t.Fatalf("раунд %d: прикрепление %v, строка %v %+v, удалены %v", round, attachErr, exists, r, deleted)
		}
	}
}

// I-2: CreateDirectInTx в транзакции, которую вызывающий откатывает, — ни строки, ни задачи; файл во входящем остаётся
// (его убирает жизненный цикл).
func TestOwnerRollbackLeavesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "admin")
	svc, ob := stageService(t, e)
	ctx := context.Background()
	st, err := svc.Stage(ctx, mediatest.PurposeImage, strings.NewReader("png"), media.Declared{ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	errRollback := errors.New("откат владельца")
	err = e.inTx(func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.x.CreateDirectInTx(ctx, tx, st, newOwner(), media.Uploader{StaffID: uuid.Must(uuid.NewV7())}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("исход: %v", err)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_objects`); n != 0 {
		t.Fatalf("строк: %d", n)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM river_job WHERE kind = 'media.process'`); n != 0 {
		t.Fatalf("задач: %d", n)
	}
	if _, err := ob.Buckets.In.Head(ctx, st.ObjectID.String()); err != nil {
		t.Fatalf("файл во входящем: %v", err)
	}
}

// stageService — сервис для Stage на Memory-бакетах (база — та же, что у env).
func stageService(t *testing.T, e env) (media.Service, blob.Opened) {
	t.Helper()
	ob, err := mediatest.MemoryBlob(e.clk)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := mediatest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := media.New(media.Deps{Pool: e.pools.As, Clock: e.clk, Registry: reg, Blob: ob, Limiter: ratelimit.Unlimited,
		Log: mediatest.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	return svc, ob
}
