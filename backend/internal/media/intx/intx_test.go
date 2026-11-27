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
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/media"
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
	// Повтор «завершить» (офлайн-очередь клиента) после открепления или снятия: объект уже не его — отказ, строка и
	// задачи не меняются (финальное ревью media, A / Task 6 m2).
	for _, st := range []media.State{media.StateDetached, media.StateTakenDown} {
		ended := e.seed(t, mediatest.Seed{State: st, Owner: a, By: by, At: at})
		if _, err := attach(ended, a, by); !errors.Is(err, media.ErrObjectUnavailable) {
			t.Errorf("повтор к %s: %v, ждали ErrObjectUnavailable", st, err)
		}
		if r, _ := object(t, e.pools.Owner, ended); r.State != string(st) || r.Version != 1 {
			t.Errorf("%s после повтора: %+v", st, r)
		}
		if n := processJobs(t, e.pools.Owner, ended); n != 0 {
			t.Errorf("%s: задач media.process %d", st, n)
		}
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

// workerPool — пул той же базы под ролью worker (исполнители чистки и обработки).
func (e env) workerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := e.pools.Owner.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE worker")
		return err
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// «Завершить» ∥ чистка окна настоящим исполнителем media.cleanup (§3.9, спека §12 «Гонки»; ревью Task 10, m2): строку
// pending_upload чистка удаляет только через сутки от created_at, входящее — сразу после окна. Часы api и воркера
// расходятся: у прикрепления окно ещё живо (последняя наносекунда), у чистки уже прошло. Строка моложе суток —
// прикрепление проходит в каждом раунде, входящее удалено или нет — по тому, кто первым взял строку. Порядок «чистка,
// затем прикрепление» оставляет объект без входящего: media.process на последней попытке — rejected(processing_failed);
// на одних часах это недостижимо.
func TestAttachVsExpiredCleanup(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "api")
	a, by := newOwner(), player()
	expires := at.Add(15 * time.Minute)
	e.clk.Set(expires.Add(time.Hour - time.Nanosecond))
	// timestamptz хранит микросекунды: чистка видит окно прошедшим через 1 мкс
	wclk := clocktest.New(expires.Add(time.Hour + time.Microsecond))
	ob, err := mediatest.MemoryBlob(wclk)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := mediatest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	deps := jobs.WorkerDeps{Pool: e.workerPool(t), Clock: wclk, Registry: reg, Blob: ob, Check: mediatest.MarkerCheck,
		Log: mediatest.Discard()}
	cw, err := jobs.NewCleanupWorker(deps)
	if err != nil {
		t.Fatal(err)
	}
	clean := func() error {
		return cw.Work(context.Background(), &river.Job[jobs.CleanupArgs]{JobRow: &rivertype.JobRow{ID: 1, Attempt: 1,
			MaxAttempts: 5, Kind: "media.cleanup"}, Args: jobs.CleanupArgs{V: 1}})
	}
	seed := func(t *testing.T) uuid.UUID {
		t.Helper()
		id := e.seed(t, mediatest.Seed{State: media.StatePendingUpload, Intended: a, By: by, ExpiresAt: expires})
		if err := ob.Buckets.In.Put(context.Background(), id.String(), strings.NewReader("png"),
			blob.PutOptions{ContentType: "image/png"}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	attach := func(id uuid.UUID) error {
		return e.inTx(func(ctx context.Context, tx pgx.Tx) error {
			_, err := e.x.AttachInTx(ctx, tx, id, a, by)
			return err
		})
	}
	inboxGone := func(t *testing.T, id uuid.UUID) bool {
		t.Helper()
		_, err := ob.Buckets.In.Head(context.Background(), id.String())
		if err != nil && !errors.Is(err, blob.ErrNotFound) {
			t.Fatal(err)
		}
		return err != nil
	}

	t.Run("race", func(t *testing.T) {
		for round := range 8 {
			id := seed(t)
			start := make(chan struct{})
			var wg sync.WaitGroup
			var attachErr, cleanErr error
			wg.Go(func() {
				<-start
				attachErr = attach(id)
			})
			wg.Go(func() {
				<-start
				cleanErr = clean()
			})
			close(start)
			wg.Wait()
			if cleanErr != nil {
				t.Fatalf("раунд %d: чистка: %v", round, cleanErr)
			}
			if r, exists := object(t, e.pools.Owner, id); attachErr != nil || !exists || r.State != "uploaded" {
				t.Fatalf("раунд %d: прикрепление %v, строка %v %+v (входящее удалено: %v)", round, attachErr, exists, r,
					inboxGone(t, id))
			}
		}
	})

	t.Run("cleanup_first", func(t *testing.T) {
		id := seed(t)
		if err := clean(); err != nil {
			t.Fatal(err)
		}
		if r, exists := object(t, e.pools.Owner, id); !exists || r.State != "pending_upload" || !inboxGone(t, id) {
			t.Fatalf("после чистки: строка %v %+v, входящее удалено %v", exists, r, inboxGone(t, id))
		}
		if err := attach(id); err != nil {
			t.Fatalf("прикрепление после чистки: %v", err)
		}
		pw, err := jobs.NewProcessWorker(deps)
		if err != nil {
			t.Fatal(err)
		}
		last := &river.Job[jobs.ProcessArgs]{JobRow: &rivertype.JobRow{ID: 2, Attempt: 10, MaxAttempts: 10,
			Kind: "media.process"}, Args: jobs.ProcessArgs{V: 1, ObjectID: id}}
		if err := pw.Work(context.Background(), last); err == nil {
			t.Fatal("media.process без входящего: ждали ошибку попытки")
		}
		var state, code string
		if err := e.pools.Owner.QueryRow(context.Background(), `SELECT state, coalesce(reject_code, '') FROM media_objects
			WHERE id = $1`, id).Scan(&state, &code); err != nil {
			t.Fatal(err)
		}
		if state != "rejected" || code != string(media.RejectProcessingFailed) {
			t.Fatalf("последняя попытка без входящего: %s %s", state, code)
		}
	})
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
