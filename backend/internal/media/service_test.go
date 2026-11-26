package media_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/media"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

var svcAt = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

// countingStore — бакет, считающий HEAD (ConfirmUpload у прикреплённого HEAD не делает).
type countingStore struct {
	blob.Store
	heads atomic.Int32
}

func (c *countingStore) Head(ctx context.Context, key string) (blob.Info, error) {
	c.heads.Add(1)
	return c.Store.Head(ctx, key)
}

type svcEnv struct {
	svc   media.Service
	pools dbtest.Pools
	clk   *clocktest.Fake
	blob  blob.Opened
	in    *countingStore
}

type svcOpt func(*media.Deps)

// newSvc — сервис под ролью api на Memory-бакетах и PG-лимитере (rate_limits), реестр — цели e2e.
func newSvc(t *testing.T, opts ...svcOpt) svcEnv {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api")
	clk := clocktest.New(svcAt)
	ob, err := mediatest.MemoryBlob(clk)
	if err != nil {
		t.Fatal(err)
	}
	in := &countingStore{Store: ob.Buckets.In}
	ob.Buckets.In = in
	reg, err := mediatest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	d := media.Deps{Pool: pools.As, Clock: clk, Registry: reg, Blob: ob,
		Limiter: ratelimit.NewPG(pools.As, clk, ratelimit.Players), Log: mediatest.Discard()}
	for _, o := range opts {
		o(&d)
	}
	svc, err := media.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return svcEnv{svc: svc, pools: pools, clk: clk, blob: d.Blob, in: in}
}

func newOwner() media.OwnerRef {
	return media.OwnerRef{Type: mediatest.OwnerType, ID: uuid.Must(uuid.NewV7())}
}

func player() media.Uploader { return media.Uploader{UserID: uuid.Must(uuid.NewV7())} }

func png(n int64) media.Declared { return media.Declared{ContentType: "image/png", Bytes: n} }

func countRows(t *testing.T, p *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// problemCode — код клиента ошибки (*httpx.Error) или "".
func problemCode(err error) string {
	var pe interface{ Problem() httpx.Problem }
	if errors.As(err, &pe) {
		return pe.Problem().Code
	}
	return ""
}

func TestNewRequiresDeps(t *testing.T) {
	t.Parallel()
	if _, err := media.New(media.Deps{}); err == nil {
		t.Fatal("New без зависимостей")
	}
}

// Режим none (§6.4): NewUpload, Stage, ConfirmUpload, PrivateURL — 503 media.unavailable; Available — false.
func TestNewUploadNone(t *testing.T) {
	t.Parallel()
	e := newSvc(t, func(d *media.Deps) { d.Blob = blob.Opened{} })
	ctx := context.Background()
	if e.svc.Available() {
		t.Fatal("Available в none")
	}
	if _, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, newOwner(), player(), png(10)); !errors.Is(err, media.ErrUnavailable) {
		t.Fatalf("NewUpload: %v", err)
	}
	if err := e.svc.ConfirmUpload(ctx, uuid.New()); !errors.Is(err, media.ErrUnavailable) {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	if _, err := e.svc.PrivateURL(ctx, uuid.New(), "full"); !errors.Is(err, media.ErrUnavailable) {
		t.Fatalf("PrivateURL: %v", err)
	}
	if n := countRows(t, e.pools.Owner, `SELECT count(*) FROM media_objects`); n != 0 {
		t.Fatalf("строк: %d", n)
	}
}

// Ранний отказ по заявленному (§1.4, §7.2): тип не из входов — unsupported_type; 0, отрицательный и больше предела —
// too_large; ровно предел — можно. Неизвестная цель и тип владельца не из цели — ошибка программиста (не код клиента).
func TestNewUploadDeclaredChecks(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	by := player()
	cases := []struct {
		name string
		d    media.Declared
		want error
	}{
		{"тип", media.Declared{ContentType: "image/gif", Bytes: 10}, media.ErrUnsupportedType},
		{"тип регистром", media.Declared{ContentType: "IMAGE/PNG", Bytes: 10}, media.ErrUnsupportedType},
		{"пустой тип", media.Declared{Bytes: 10}, media.ErrUnsupportedType},
		{"0 байт", png(0), media.ErrTooLarge},
		{"минус", png(-1), media.ErrTooLarge},
		{"предел+1", png(mediatest.MaxBytes + 1), media.ErrTooLarge},
	}
	for _, c := range cases {
		if _, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, newOwner(), by, c.d); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, newOwner(), by, png(mediatest.MaxBytes)); err != nil {
		t.Fatalf("ровно предел: %v", err)
	}
	for name, call := range map[string]func() error{
		"цель": func() error {
			_, err := e.svc.NewUpload(ctx, "nope", newOwner(), by, png(10))
			return err
		},
		"тип владельца": func() error {
			_, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, media.OwnerRef{Type: "other", ID: uuid.New()}, by, png(10))
			return err
		},
		"два загрузивших": func() error {
			_, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, newOwner(), media.Uploader{UserID: uuid.New(), StaffID: uuid.New()}, png(10))
			return err
		},
	} {
		if err := call(); err == nil || problemCode(err) != "" {
			t.Errorf("%s: %v — нужна ошибка программиста без кода клиента", name, err)
		}
	}
	if n := countRows(t, e.pools.Owner, `SELECT count(*) FROM media_objects`); n != 1 {
		t.Fatalf("строк: %d, want 1", n)
	}
}

// Билет (§3.1 п. 2, R3): POST, срок 15 мин, CompleteBefore = ExpiresAt + 1 ч, типы входов цели; поля формы подписаны
// ровно на заявленный размер и тип; строки — объект pending_upload от игрока и билет на намеченного владельца.
func TestTicketShape(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	o, by := newOwner(), player()
	tk, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, o, by, png(1234))
	if err != nil {
		t.Fatal(err)
	}
	if tk.Method != http.MethodPost || tk.MaxBytes != 1234 || tk.URL != mediatest.BaseURL+"/_blob/in" {
		t.Fatalf("билет: %+v", tk)
	}
	if !tk.ExpiresAt.Equal(svcAt.Add(15*time.Minute)) || !tk.CompleteBefore.Equal(tk.ExpiresAt.Add(time.Hour)) {
		t.Fatalf("сроки: %v %v", tk.ExpiresAt, tk.CompleteBefore)
	}
	if !slices.Equal(tk.AcceptedTypes, []string{"image/jpeg", "image/png", "image/webp"}) {
		t.Fatalf("типы: %v", tk.AcceptedTypes)
	}
	key, err := e.blob.Dev.VerifyUpload(tk.Fields, 1234, "image/png")
	if err != nil || key != tk.UploadID.String() {
		t.Fatalf("подпись: %q %v", key, err)
	}
	for _, bad := range []struct {
		size int64
		ct   string
	}{{1233, "image/png"}, {1235, "image/png"}, {1234, "image/jpeg"}} {
		if _, err := e.blob.Dev.VerifyUpload(tk.Fields, bad.size, bad.ct); !errors.Is(err, blob.ErrSignature) {
			t.Errorf("%d %s: %v", bad.size, bad.ct, err)
		}
	}
	var state, ot, method string
	var oid uuid.UUID
	var ub *uuid.UUID
	var maxBytes, declared int64
	var exp time.Time
	if err := e.pools.Owner.QueryRow(ctx, `SELECT o.state, o.uploaded_by_user, o.declared_bytes, u.intended_owner_type,
		u.intended_owner_id, u.method, u.max_bytes, u.expires_at FROM media_objects o JOIN media_uploads u ON u.object_id = o.id
		WHERE o.id = $1`, tk.UploadID).Scan(&state, &ub, &declared, &ot, &oid, &method, &maxBytes, &exp); err != nil {
		t.Fatal(err)
	}
	if state != "pending_upload" || ub == nil || *ub != by.UserID || declared != 1234 || ot != o.Type || oid != o.ID ||
		method != "POST" || maxBytes != 1234 || !exp.Equal(tk.ExpiresAt) {
		t.Fatalf("строки: %s %v %d %s %s %s %d %v", state, ub, declared, ot, oid, method, maxBytes, exp)
	}
}

// m5: 10 параллельных «начать» одного игрока — ровно 5 билетов, остальные too_many_pending, без 40P01. Просроченные
// билеты не мешают.
func TestNewUploadPendingLimitConcurrent(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	by := player()
	start := make(chan struct{})
	errs := make([]error, 10)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() {
			<-start
			_, errs[i] = e.svc.NewUpload(ctx, mediatest.PurposeImage, newOwner(), by, png(10))
		})
	}
	close(start)
	wg.Wait()
	ok, pending := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, media.ErrTooManyPending):
			pending++
		default:
			t.Errorf("ошибка: %v", err)
		}
	}
	if ok != 5 || pending != 5 {
		t.Fatalf("успехов %d, too_many_pending %d", ok, pending)
	}
	// срок билета (15 мин) прошёл — живых нет, снова можно
	e.clk.Advance(15 * time.Minute)
	if _, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, newOwner(), by, png(10)); err != nil {
		t.Fatalf("после срока: %v", err)
	}
}

// Сотрудник и система лимитов игрока не считают (§3.11: сотрудник — общий класс admin-api).
func TestNewUploadStaffSkipsPlayerLimits(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	staff := media.Uploader{StaffID: uuid.Must(uuid.NewV7())}
	for i := range 7 {
		if _, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, newOwner(), staff, png(mediatest.MaxBytes)); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
}

// denyLimiter — отказывает и запоминает ключ и политику.
type denyLimiter struct {
	mu   sync.Mutex
	keys []string
	pols []ratelimit.Policy
}

func (d *denyLimiter) Allow(_ context.Context, key string, p ratelimit.Policy) (ratelimit.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.keys, d.pols = append(d.keys, key), append(d.pols, p)
	return ratelimit.Result{RetryAfter: 90 * time.Second}, nil
}
func (*denyLimiter) Add(context.Context, string, ratelimit.Policy) error          { return nil }
func (*denyLimiter) Over(context.Context, string, ratelimit.Policy) (bool, error) { return false, nil }

// R19: 100 билетов в сутки — счёт Limiter по ключу субъекта класса media_upload_day; отказ — 429 ratelimit.exceeded с
// Retry-After, объекта нет.
func TestNewUploadDayCount(t *testing.T) {
	t.Parallel()
	lim := &denyLimiter{}
	e := newSvc(t, func(d *media.Deps) { d.Limiter = lim })
	by := player()
	_, err := e.svc.NewUpload(context.Background(), mediatest.PurposeImage, newOwner(), by, png(10))
	var pe interface{ Problem() httpx.Problem }
	if !errors.As(err, &pe) || pe.Problem().Code != httpx.CodeRateLimited || pe.Problem().RetryAfter != 90 {
		t.Fatalf("ошибка: %v", err)
	}
	want := ratelimit.Policy{Limit: 100, Period: 24 * time.Hour}
	if len(lim.keys) != 1 || lim.keys[0] != "user:media_upload_day:"+by.UserID.String() || lim.pols[0] != want {
		t.Fatalf("лимитер: %v %v", lim.keys, lim.pols)
	}
	if n := countRows(t, e.pools.Owner, `SELECT count(*) FROM media_objects`); n != 0 {
		t.Fatalf("строк: %d", n)
	}
}

// R19: объём по declared_bytes за сутки ≤ 200 МиБ — ровно предел можно, сверх — quota_exceeded (объекта нет); через
// 24 ч − 1 с ещё нельзя, через 24 ч + 1 с — снова можно. Цель с пределом 150 МиБ — чтобы предел набирался двумя
// билетами (иначе мешал бы предел незавершённых).
func TestNewUploadQuotaBytes(t *testing.T) {
	t.Parallel()
	big := media.Purpose{Name: "big", Visibility: media.VisibilityPublic, Inputs: []media.InputKind{media.InputPNG},
		MaxBytes: 150 << 20, MaxPixels: 1, Variants: []media.Variant{{Name: "full"}}, OwnerTypes: []string{mediatest.OwnerType}}
	reg, err := media.NewRegistry(big)
	if err != nil {
		t.Fatal(err)
	}
	e := newSvc(t, func(d *media.Deps) { d.Registry = reg })
	ctx := context.Background()
	by := player()
	up := func(n int64) error {
		_, err := e.svc.NewUpload(ctx, "big", newOwner(), by, png(n))
		return err
	}
	if err := up(150 << 20); err != nil {
		t.Fatal(err)
	}
	if err := up(50<<20 + 1); !errors.Is(err, media.ErrQuotaExceeded) {
		t.Fatalf("сверх предела: %v", err)
	}
	if err := up(50 << 20); err != nil {
		t.Fatalf("ровно предел: %v", err)
	}
	if err := up(1); !errors.Is(err, media.ErrQuotaExceeded) {
		t.Fatalf("после предела: %v", err)
	}
	// другой игрок не задет
	if _, err := e.svc.NewUpload(ctx, "big", newOwner(), player(), png(1)); err != nil {
		t.Fatalf("другой игрок: %v", err)
	}
	e.clk.Set(svcAt.Add(24*time.Hour - time.Second))
	if err := up(1); !errors.Is(err, media.ErrQuotaExceeded) {
		t.Fatalf("24 ч − 1 с: %v", err)
	}
	e.clk.Set(svcAt.Add(24*time.Hour + time.Second))
	if err := up(1); err != nil {
		t.Fatalf("24 ч + 1 с: %v", err)
	}
	if n := countRows(t, e.pools.Owner, `SELECT count(*) FROM media_objects WHERE uploaded_by_user = $1`, by.UserID); n != 3 {
		t.Fatalf("объектов игрока: %d, want 3", n)
	}
}

// §3.1 п. 5, m1, m2: файла нет — upload_incomplete; окно строго до expires_at + 1 ч; не pending_upload (прикреплён или
// строки нет) — nil без HEAD.
func TestConfirmUpload(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	o, by := newOwner(), player()
	tk, err := e.svc.NewUpload(ctx, mediatest.PurposeImage, o, by, png(3))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ConfirmUpload(ctx, tk.UploadID); !errors.Is(err, media.ErrUploadIncomplete) {
		t.Fatalf("без файла: %v", err)
	}
	if err := e.blob.Buckets.In.Put(ctx, tk.UploadID.String(), bytes.NewReader([]byte("abc")), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ConfirmUpload(ctx, tk.UploadID); err != nil {
		t.Fatalf("с файлом: %v", err)
	}
	e.clk.Set(tk.ExpiresAt.Add(time.Hour - time.Nanosecond))
	if err := e.svc.ConfirmUpload(ctx, tk.UploadID); err != nil {
		t.Fatalf("окно − 1 нс: %v", err)
	}
	e.clk.Set(tk.ExpiresAt.Add(time.Hour))
	if err := e.svc.ConfirmUpload(ctx, tk.UploadID); !errors.Is(err, media.ErrUploadExpired) {
		t.Fatalf("окно: %v", err)
	}
	// прикреплён (билета нет) и строки нет — nil без HEAD
	attached, err := mediatest.SeedObject(ctx, e.pools.Owner, mediatest.Seed{Purpose: mediatest.PurposeImage,
		Visibility: media.VisibilityPublic, State: media.StateUploaded, Owner: o, By: by, At: svcAt})
	if err != nil {
		t.Fatal(err)
	}
	before := e.in.heads.Load()
	for _, id := range []uuid.UUID{attached, uuid.New()} {
		if err := e.svc.ConfirmUpload(ctx, id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if got := e.in.heads.Load(); got != before {
		t.Fatalf("HEAD у не pending_upload: %d", got-before)
	}
}

// Stage (§2.3): байты во входящем по ключу нового object_id, записи в базе нет.
func TestStageNoDB(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	st, err := e.svc.Stage(ctx, mediatest.PurposeImage, strings.NewReader("hello"), media.Declared{ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if st.ObjectID == uuid.Nil || st.Purpose != mediatest.PurposeImage || st.Bytes != 5 || st.ContentType != "image/png" {
		t.Fatalf("staged: %+v", st)
	}
	rc, info, err := e.blob.Buckets.In.Get(ctx, st.ObjectID.String())
	if err != nil || info.Size != 5 || info.ContentType != "image/png" {
		t.Fatalf("входящее: %+v %v", info, err)
	}
	_ = rc.Close()
	if n := countRows(t, e.pools.Owner, `SELECT count(*) FROM media_objects`); n != 0 {
		t.Fatalf("строк: %d", n)
	}
}

// Stage: больше предела цели (по байтам, не по заявке) и пустое — too_large; тип не из входов — unsupported_type;
// входящее не тронуто.
func TestStageTooLarge(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	over := bytes.NewReader(make([]byte, mediatest.MaxBytes+1))
	if _, err := e.svc.Stage(ctx, mediatest.PurposeImage, over, media.Declared{ContentType: "image/png", Bytes: 10}); !errors.Is(err, media.ErrTooLarge) {
		t.Fatalf("больше предела: %v", err)
	}
	if _, err := e.svc.Stage(ctx, mediatest.PurposeImage, strings.NewReader(""), media.Declared{ContentType: "image/png"}); !errors.Is(err, media.ErrTooLarge) {
		t.Fatalf("пусто: %v", err)
	}
	if _, err := e.svc.Stage(ctx, mediatest.PurposeImage, strings.NewReader("x"), media.Declared{ContentType: "text/html"}); !errors.Is(err, media.ErrUnsupportedType) {
		t.Fatalf("тип: %v", err)
	}
	exact := bytes.NewReader(make([]byte, mediatest.MaxBytes))
	if _, err := e.svc.Stage(ctx, mediatest.PurposeImage, exact, media.Declared{ContentType: "image/png"}); err != nil {
		t.Fatalf("ровно предел: %v", err)
	}
	n := 0
	if err := e.blob.Buckets.In.List(ctx, "", func(string) error { n++; return nil }); err != nil || n != 1 {
		t.Fatalf("входящее: %d %v", n, err)
	}
}

func TestStageNone(t *testing.T) {
	t.Parallel()
	e := newSvc(t, func(d *media.Deps) { d.Blob = blob.Opened{} })
	if _, err := e.svc.Stage(context.Background(), mediatest.PurposeImage, strings.NewReader("x"), media.Declared{ContentType: "image/png"}); !errors.Is(err, media.ErrUnavailable) {
		t.Fatalf("none: %v", err)
	}
}

// §3.4, §3.5: публичный ready — адреса PublicURL; у закрытого в Image адресов нет, PrivateURL — подписанная ссылка на
// 5 минут (после срока VerifyGet отвергает); публичный, не ready, нет варианта, нет строки — object_unavailable.
func TestPrivateURL(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	seed := func(purpose string, v media.Visibility, s media.State) uuid.UUID {
		id, err := mediatest.SeedObject(ctx, e.pools.Owner, mediatest.Seed{Purpose: purpose, Visibility: v, State: s,
			Owner: newOwner(), By: player(), At: svcAt, Variants: []string{"full", "thumb"}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	priv := seed(mediatest.PurposeImagePrivate, media.VisibilityPrivate, media.StateReady)
	pub := seed(mediatest.PurposeImage, media.VisibilityPublic, media.StateReady)
	privTaken := seed(mediatest.PurposeImagePrivate, media.VisibilityPrivate, media.StateTakenDown)

	link, err := e.svc.PrivateURL(ctx, priv, "full")
	if err != nil {
		t.Fatal(err)
	}
	if !link.ExpiresAt.Equal(svcAt.Add(5 * time.Minute)) {
		t.Fatalf("срок: %v", link.ExpiresAt)
	}
	u, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	key := mediatest.VariantKey(priv, "full")
	if u.Path != "/_media/priv/"+key {
		t.Fatalf("путь: %s", u.Path)
	}
	exp, sig := u.Query().Get("exp"), u.Query().Get("sig")
	if err := e.blob.Dev.VerifyGet(key, exp, sig); err != nil {
		t.Fatalf("подпись: %v", err)
	}
	e.clk.Advance(5 * time.Minute)
	if err := e.blob.Dev.VerifyGet(key, exp, sig); !errors.Is(err, blob.ErrExpired) {
		t.Fatalf("после срока: %v", err)
	}
	for name, c := range map[string]struct {
		id      uuid.UUID
		variant string
	}{"публичный": {pub, "full"}, "снятый": {privTaken, "full"}, "нет варианта": {priv, "huge"}, "нет строки": {uuid.New(), "full"}} {
		if _, err := e.svc.PrivateURL(ctx, c.id, c.variant); !errors.Is(err, media.ErrObjectUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}

	img, ok, err := e.svc.Image(ctx, priv)
	if err != nil || !ok || img.State != media.StateReady || len(img.Variants) != 2 {
		t.Fatalf("Image закрытого: %+v %v %v", img, ok, err)
	}
	for name, v := range img.Variants {
		if v.URL != "" {
			t.Errorf("закрытый вариант %s с адресом %q", name, v.URL)
		}
	}
	img, ok, err = e.svc.Image(ctx, pub)
	if err != nil || !ok || img.Variants["full"].URL != mediatest.PublicBaseURL+mediatest.VariantKey(pub, "full") ||
		img.Variants["thumb"].Width != 10 || img.Variants["thumb"].Height != 8 || img.Variants["thumb"].ContentType != "image/webp" {
		t.Fatalf("Image публичного: %+v %v %v", img, ok, err)
	}
	if e.svc.PublicURL("a/b.webp") != mediatest.PublicBaseURL+"a/b.webp" {
		t.Fatal("PublicURL")
	}
}

// §7.1: uploaded и processing — «processing» без вариантов, rejected — с кодом без вариантов, снятый — без вариантов;
// нет строки — (zero, false, nil); Images — пачкой, отсутствующих нет в карте.
func TestImages(t *testing.T) {
	t.Parallel()
	e := newSvc(t)
	ctx := context.Background()
	ids := map[media.State]uuid.UUID{}
	for _, s := range []media.State{media.StateUploaded, media.StateProcessing, media.StateRejected, media.StateTakenDown, media.StateReady} {
		id, err := mediatest.SeedObject(ctx, e.pools.Owner, mediatest.Seed{Purpose: mediatest.PurposeImage,
			Visibility: media.VisibilityPublic, State: s, Owner: newOwner(), By: player(), At: svcAt, Variants: []string{"full"}})
		if err != nil {
			t.Fatal(err)
		}
		ids[s] = id
	}
	missing := uuid.New()
	got, err := e.svc.Images(ctx, []uuid.UUID{ids[media.StateUploaded], ids[media.StateProcessing], ids[media.StateRejected],
		ids[media.StateTakenDown], ids[media.StateReady], missing})
	if err != nil || len(got) != 5 {
		t.Fatalf("Images: %d %v", len(got), err)
	}
	want := map[media.State]media.State{media.StateUploaded: media.StateProcessing, media.StateProcessing: media.StateProcessing,
		media.StateRejected: media.StateRejected, media.StateTakenDown: media.StateTakenDown, media.StateReady: media.StateReady}
	for s, id := range ids {
		img := got[id]
		if img.ID != id || img.State != want[s] || (s == media.StateReady) != (len(img.Variants) == 1) {
			t.Errorf("%s: %+v", s, img)
		}
	}
	if got[ids[media.StateRejected]].RejectCode != media.RejectCorrupt {
		t.Errorf("код: %+v", got[ids[media.StateRejected]])
	}
	if img, ok, err := e.svc.Image(ctx, missing); ok || err != nil || img.ID != uuid.Nil {
		t.Fatalf("нет строки: %+v %v %v", img, ok, err)
	}
	if m, err := e.svc.Images(ctx, nil); err != nil || len(m) != 0 {
		t.Fatalf("пусто: %v %v", m, err)
	}
}

// R19 под замком (m5): два параллельных «начать» по 150 МиБ при пределе 200 МиБ — ровно один билет.
func TestNewUploadQuotaConcurrent(t *testing.T) {
	t.Parallel()
	big := media.Purpose{Name: "big", Visibility: media.VisibilityPublic, Inputs: []media.InputKind{media.InputPNG},
		MaxBytes: 150 << 20, MaxPixels: 1, Variants: []media.Variant{{Name: "full"}}, OwnerTypes: []string{mediatest.OwnerType}}
	reg, err := media.NewRegistry(big)
	if err != nil {
		t.Fatal(err)
	}
	e := newSvc(t, func(d *media.Deps) { d.Registry = reg })
	by := player()
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() {
			<-start
			_, errs[i] = e.svc.NewUpload(context.Background(), "big", newOwner(), by, png(150<<20))
		})
	}
	close(start)
	wg.Wait()
	ok, quota := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, media.ErrQuotaExceeded):
			quota++
		default:
			t.Errorf("ошибка: %v", err)
		}
	}
	if ok != 1 || quota != 1 {
		t.Fatalf("успехов %d, quota_exceeded %d", ok, quota)
	}
}
