package jobs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
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
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/media"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

var at = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

// Цели тестов сверх e2e: маленькая растровая (too_large, too_many_pixels) и Lottie.
const (
	purposeTiny  = "t_tiny"
	purposeAnim  = "t_anim"
	tinyMaxPix   = 10_000
	animVariant  = "anim"
	cacheControl = "public, max-age=86400"
)

func tinyPurpose(maxBytes int64) media.Purpose {
	return media.Purpose{Name: purposeTiny, Visibility: media.VisibilityPublic, Inputs: []media.InputKind{media.InputPNG},
		MaxBytes: maxBytes, MaxPixels: tinyMaxPix, Variants: []media.Variant{{Name: "full", MaxSide: 64, Lossy: true, Quality: 80}},
		OwnerTypes: []string{mediatest.OwnerType}}
}

func animPurpose() media.Purpose {
	return media.Purpose{Name: purposeAnim, Visibility: media.VisibilityPublic, Inputs: []media.InputKind{media.InputLottie},
		MaxBytes: 1 << 20, Variants: []media.Variant{{Name: animVariant}}, OwnerTypes: []string{mediatest.OwnerType}}
}

// recPurger — Purger, записывающий адреса.
type recPurger struct {
	mu   sync.Mutex
	urls []string
}

func (p *recPurger) Purge(_ context.Context, urls []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.urls = append(p.urls, urls...)
	return nil
}

func (p *recPurger) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.urls)
}

// hookStore — Store с перехватами: счёт вызовов и подмена Get/Put.
type hookStore struct {
	blob.Store
	mu    sync.Mutex
	calls map[string]int
	get   func(ctx context.Context, key string) (io.ReadCloser, blob.Info, error)
	put   func(ctx context.Context, key string, n int) // после успешного Put, n — номер вызова
	// before — до записи Put (n — номер вызова); ошибка — Put не пишет и возвращает её
	before func(ctx context.Context, key string, n int) error
}

func wrap(s blob.Store) *hookStore { return &hookStore{Store: s, calls: map[string]int{}} }

func (h *hookStore) inc(op string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls[op]++
	return h.calls[op]
}

func (h *hookStore) count(op string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[op]
}

func (h *hookStore) total() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		n += c
	}
	return n
}

func (h *hookStore) Put(ctx context.Context, key string, r io.Reader, o blob.PutOptions) error {
	n := h.inc("put")
	if h.before != nil {
		if err := h.before(ctx, key, n); err != nil {
			return err
		}
	}
	if err := h.Store.Put(ctx, key, r, o); err != nil {
		return err
	}
	if h.put != nil {
		h.put(ctx, key, n)
	}
	return nil
}

func (h *hookStore) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	h.inc("get")
	if h.get != nil {
		return h.get(ctx, key)
	}
	return h.Store.Get(ctx, key)
}

func (h *hookStore) Delete(ctx context.Context, key string) error {
	h.inc("delete")
	return h.Store.Delete(ctx, key)
}

func (h *hookStore) Head(ctx context.Context, key string) (blob.Info, error) {
	h.inc("head")
	return h.Store.Head(ctx, key)
}

type env struct {
	pools  dbtest.Pools // As — роль worker
	clk    *clocktest.Fake
	reg    *media.Registry
	mem    blob.Buckets // настоящие бакеты в памяти
	in     *hookStore
	pub    *hookStore
	priv   *hookStore
	purger *recPurger
	deps   jobs.WorkerDeps
	pw     *jobs.ProcessWorker
	gw     *jobs.PurgeWorker
	cw     *jobs.CleanupWorker
}

type envOpt struct {
	extra []media.Purpose
	check media.ContentCheck
	log   *recLog // nil — журнал в никуда
}

// recLog — журнал исполнителей, запоминающий «УРОВЕНЬ сообщение».
type recLog struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recLog) Enabled(context.Context, slog.Level) bool { return true }

func (r *recLog) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, rec.Level.String()+" "+rec.Message)
	return nil
}

func (r *recLog) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recLog) WithGroup(string) slog.Handler      { return r }

func (r *recLog) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.msgs)
}

// newEnv — исполнители под ролью worker на памяти с перехватами бакетов.
func newEnv(t *testing.T, o envOpt) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "worker")
	clk := clocktest.New(at)
	reg, err := media.NewRegistry(append(mediatest.E2EPurposes(), o.extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := mediatest.MemoryBlob(clk)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{pools: pools, clk: clk, reg: reg, mem: opened.Buckets, purger: &recPurger{}}
	e.in, e.pub, e.priv = wrap(opened.Buckets.In), wrap(opened.Buckets.Pub), wrap(opened.Buckets.Priv)
	opened.Buckets = blob.Buckets{In: e.in, Pub: e.pub, Priv: e.priv}
	opened.Purger = e.purger
	check := o.check
	if check == nil {
		check = media.CheckOff
	}
	log := mediatest.Discard()
	if o.log != nil {
		log = slog.New(o.log)
	}
	e.deps = jobs.WorkerDeps{Pool: pools.As, Clock: clk, Registry: reg, Blob: opened, Check: check, Log: log}
	if e.pw, err = jobs.NewProcessWorker(e.deps); err != nil {
		t.Fatal(err)
	}
	if e.gw, err = jobs.NewPurgeWorker(e.deps); err != nil {
		t.Fatal(err)
	}
	if e.cw, err = jobs.NewCleanupWorker(e.deps); err != nil {
		t.Fatal(err)
	}
	return e
}

func newOwner() media.OwnerRef {
	return media.OwnerRef{Type: mediatest.OwnerType, ID: uuid.Must(uuid.NewV7())}
}

// seed — объект в состоянии st (по умолчанию uploaded) с входящим файлом data (nil — без файла).
func (e *env) seed(t *testing.T, purpose string, st media.State, data []byte, variants ...string) uuid.UUID {
	t.Helper()
	p, ok := e.reg.Get(purpose)
	if !ok {
		t.Fatalf("цель %s", purpose)
	}
	oid, err := mediatest.SeedObject(context.Background(), e.pools.Owner, mediatest.Seed{Purpose: purpose,
		Visibility: p.Visibility, State: st, Owner: newOwner(), By: media.Uploader{UserID: uuid.Must(uuid.NewV7())},
		At: at, Variants: variants})
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		if err := e.mem.In.Put(context.Background(), oid.String(), bytes.NewReader(data), blob.PutOptions{ContentType: "image/png"}); err != nil {
			t.Fatal(err)
		}
	}
	return oid
}

func job(oid uuid.UUID, attempt int) *river.Job[jobs.ProcessArgs] {
	return &river.Job[jobs.ProcessArgs]{JobRow: &rivertype.JobRow{ID: 1, Attempt: attempt, MaxAttempts: 10, Kind: "media.process"},
		Args: jobs.ProcessArgs{V: 1, ObjectID: oid}}
}

type objRow struct {
	State      string
	RejectCode *string
	Attempts   int32
	Width      *int32
	Height     *int32
	Version    int64
}

func (e *env) object(t *testing.T, oid uuid.UUID) objRow {
	t.Helper()
	var r objRow
	if err := e.pools.Owner.QueryRow(context.Background(), `SELECT state, reject_code, process_attempts, width, height, version
		FROM media_objects WHERE id = $1`, oid).Scan(&r.State, &r.RejectCode, &r.Attempts, &r.Width, &r.Height, &r.Version); err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, p *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) events(t *testing.T, oid uuid.UUID, typ string) int {
	t.Helper()
	return count(t, e.pools.Owner, `SELECT count(*) FROM outbox WHERE aggregate_id = $1 AND event_type = $2`, oid, typ)
}

// purgeJobs — аргументы задач media.purge по объекту (ключ — <id> или <id>/…).
func (e *env) purgeJobs(t *testing.T, oid uuid.UUID) []jobs.PurgeArgs {
	t.Helper()
	rows, err := e.pools.Owner.Query(context.Background(), `SELECT args, queue, max_attempts FROM river_job WHERE kind = 'media.purge' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []jobs.PurgeArgs
	for rows.Next() {
		var raw []byte
		var q string
		var maxAttempts int
		if err := rows.Scan(&raw, &q, &maxAttempts); err != nil {
			t.Fatal(err)
		}
		var a jobs.PurgeArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(a.Keys, func(k string) bool { return k == oid.String() || strings.HasPrefix(k, oid.String()+"/") }) {
			if q != "media" || maxAttempts != 25 || a.V != 1 {
				t.Fatalf("purge: очередь %s, попыток %d, v %d", q, maxAttempts, a.V)
			}
			out = append(out, a)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// runPurges — исполнить вставленные задачи media.purge объекта.
func (e *env) runPurges(t *testing.T, oid uuid.UUID) {
	t.Helper()
	for _, a := range e.purgeJobs(t, oid) {
		j := &river.Job[jobs.PurgeArgs]{JobRow: &rivertype.JobRow{ID: 2, Attempt: 1, MaxAttempts: 25}, Args: a}
		if err := e.gw.Work(context.Background(), j); err != nil {
			t.Fatalf("purge %+v: %v", a, err)
		}
	}
}

// keys — ключи бакета с префиксом <id>/.
func keys(t *testing.T, s blob.Store, oid uuid.UUID) []string {
	t.Helper()
	var out []string
	if err := s.List(context.Background(), oid.String()+"/", func(k string) error { out = append(out, k); return nil }); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func inExists(t *testing.T, s blob.Store, oid uuid.UUID) bool {
	t.Helper()
	_, err := s.Head(context.Background(), oid.String())
	if errors.Is(err, blob.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

func encPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.NRGBA{R: byte(x * 255 / w & 0xFF), G: byte(y * 255 / h & 0xFF), B: 128, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// lockedTx — транзакция владельца базы, держащая FOR UPDATE строки объекта; release — коммит.
func lockedTx(t *testing.T, p *pgxpool.Pool, oid uuid.UUID) (release func()) {
	t.Helper()
	ctx := context.Background()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM media_objects WHERE id = $1 FOR UPDATE`, oid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			if err := tx.Commit(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error(err)
			}
		})
	}
	t.Cleanup(release)
	return release
}
