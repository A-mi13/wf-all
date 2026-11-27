package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/media"
	"wf/backend/internal/media/intx"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/rivertest"
)

// happy — uploaded → ready: варианты в бакете видимости с Cache-Control, строки вариантов, событие, входящее удалено.
func happy(t *testing.T, purpose string, private bool) {
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	oid := e.seed(t, purpose, media.StateUploaded, encPNG(t, 640, 480))
	if err := e.pw.Work(ctx, job(oid, 1)); err != nil {
		t.Fatal(err)
	}
	o := e.object(t, oid)
	if o.State != "ready" || o.Width == nil || *o.Width != 640 || o.Height == nil || *o.Height != 480 || o.Attempts != 0 {
		t.Fatalf("объект: %+v", o)
	}
	want := []string{mediatest.VariantKey(oid, "full"), mediatest.VariantKey(oid, "thumb")}
	bucket, other, cc := e.mem.Pub, e.mem.Priv, cacheControl
	if private {
		bucket, other, cc = e.mem.Priv, e.mem.Pub, "private, no-store"
	}
	if got := keys(t, bucket, oid); !slices.Equal(got, want) {
		t.Fatalf("ключи: %v", got)
	}
	if got := keys(t, other, oid); len(got) != 0 {
		t.Fatalf("варианты в чужом бакете: %v", got)
	}
	for _, k := range want {
		info, err := bucket.Head(ctx, k)
		if err != nil || info.CacheControl != cc || info.ContentType != "image/webp" {
			t.Fatalf("%s: %+v %v", k, info, err)
		}
	}
	rows, err := e.pools.Owner.Query(ctx, `SELECT name, blob_key, content_type, width, height, bytes FROM media_variants
		WHERE object_id = $1 ORDER BY name`, oid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name, key, ct string
		var w, h int32
		var n int64
		if err := rows.Scan(&name, &key, &ct, &w, &h, &n); err != nil {
			t.Fatal(err)
		}
		info, err := bucket.Head(ctx, key)
		if err != nil || key != mediatest.VariantKey(oid, name) || ct != "image/webp" || n != info.Size || w <= 0 || h <= 0 {
			t.Fatalf("строка варианта %s: %s %s %d×%d %d (%+v %v)", name, key, ct, w, h, n, info, err)
		}
		names = append(names, name)
	}
	if !slices.Equal(names, []string{"full", "thumb"}) {
		t.Fatalf("строки вариантов: %v", names)
	}
	if n := e.events(t, oid, media.EventObjectReady); n != 1 {
		t.Fatalf("событий ready: %d", n)
	}
	if inExists(t, e.mem.In, oid) {
		t.Fatal("входящее не удалено")
	}
	if p := e.purgeJobs(t, oid); len(p) != 0 {
		t.Fatalf("media.purge у готового: %+v", p)
	}
}

func TestProcessHappyPublic(t *testing.T) {
	t.Parallel()
	happy(t, mediatest.PurposeImage, false)
}

func TestProcessHappyPrivate(t *testing.T) {
	t.Parallel()
	happy(t, mediatest.PurposeImagePrivate, true)
}

// Lottie: один вариант <id>/anim.json, application/json, размеры из анимации.
func TestProcessHappyLottie(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{extra: []media.Purpose{animPurpose()}})
	data, err := os.ReadFile("../internal/lottie/testdata/lac-path.json")
	if err != nil {
		t.Fatal(err)
	}
	oid := e.seed(t, purposeAnim, media.StateUploaded, data)
	if err := e.pw.Work(context.Background(), job(oid, 1)); err != nil {
		t.Fatal(err)
	}
	o := e.object(t, oid)
	if o.State != "ready" || o.Width == nil || *o.Width != 512 || *o.Height != 512 {
		t.Fatalf("объект: %+v", o)
	}
	key := oid.String() + "/anim.json"
	info, err := e.mem.Pub.Head(context.Background(), key)
	if err != nil || info.ContentType != "application/json" || info.CacheControl != cacheControl {
		t.Fatalf("anim.json: %+v %v", info, err)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_variants WHERE object_id = $1 AND name = $2 AND blob_key = $3
		AND content_type = 'application/json'`, oid, animVariant, key); n != 1 {
		t.Fatalf("строк варианта: %d", n)
	}
}

// Отказы шагов 2–5 → rejected(код), событие, media.purge входящего, вариантов нет.
func TestProcessRejectCodes(t *testing.T) {
	t.Parallel()
	pngOK := encPNG(t, 64, 64)
	cases := []struct {
		name, purpose string
		data          []byte
		code          media.RejectCode
	}{
		{"unsupported_type", mediatest.PurposeImage, []byte("<html><script>alert(1)</script></html>"), media.RejectUnsupportedType},
		{"too_many_pixels", purposeTiny, encPNG(t, 200, 200), media.RejectTooManyPixels},
		{"corrupt", mediatest.PurposeImage, append(slices.Clone(pngOK[:40]), bytes.Repeat([]byte{0}, 40)...), media.RejectCorrupt},
		{"content_rejected", mediatest.PurposeImage, encPNG(t, mediatest.MarkerSide, mediatest.MarkerSide), media.RejectContentRejected},
		{"unsafe_animation", purposeAnim, []byte(`{"v":"5.7.4","fr":30,"ip":0,"op":60,"w":100,"h":100,"layers":[{"ty":4,"ip":0,"op":60,"ks":{"o":{"a":0,"k":100,"x":"var $bm_rt = time;"}},"shapes":[]}]}`), media.RejectUnsafeAnimation},
		{"corrupt_lottie", purposeAnim, []byte(`{"v":`), media.RejectCorrupt},
		{"too_large", purposeTiny, bytes.Repeat([]byte{1}, 4097), media.RejectTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, envOpt{extra: []media.Purpose{tinyPurpose(4096), animPurpose()}, check: mediatest.MarkerCheck})
			oid := e.seed(t, c.purpose, media.StateUploaded, c.data)
			if err := e.pw.Work(context.Background(), job(oid, 1)); err != nil {
				t.Fatal(err)
			}
			o := e.object(t, oid)
			if o.State != "rejected" || o.RejectCode == nil || *o.RejectCode != string(c.code) {
				t.Fatalf("объект: %+v (код %v)", o, deref(o.RejectCode))
			}
			if n := e.events(t, oid, media.EventObjectRejected); n != 1 {
				t.Fatalf("событий rejected: %d", n)
			}
			if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_variants WHERE object_id = $1`, oid); n != 0 {
				t.Fatalf("строк вариантов: %d", n)
			}
			if got := append(keys(t, e.mem.Pub, oid), keys(t, e.mem.Priv, oid)...); len(got) != 0 {
				t.Fatalf("файлы вариантов: %v", got)
			}
			ps := e.purgeJobs(t, oid)
			if !slices.ContainsFunc(ps, func(a jobs.PurgeArgs) bool {
				return a.Bucket == jobs.BucketIn && slices.Equal(a.Keys, []string{oid.String()})
			}) {
				t.Fatalf("нет media.purge входящего: %+v", ps)
			}
			e.runPurges(t, oid)
			if inExists(t, e.mem.In, oid) {
				t.Fatal("входящее осталось после media.purge")
			}
		})
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// Перезаливка во входящее по живому билету между ConfirmUpload и задачей: предел — по байтам, а не по заявке.
// Ровно MaxBytes проходит, MaxBytes + 1 — too_large.
func TestProcessTooLargeAfterReupload(t *testing.T) {
	t.Parallel()
	small := encPNG(t, 32, 32)
	e := newEnv(t, envOpt{extra: []media.Purpose{tinyPurpose(int64(len(small)))}})
	ctx := context.Background()
	exact := e.seed(t, purposeTiny, media.StateUploaded, small)
	if err := e.pw.Work(ctx, job(exact, 1)); err != nil {
		t.Fatal(err)
	}
	if o := e.object(t, exact); o.State != "ready" {
		t.Fatalf("ровно MaxBytes: %+v", o)
	}
	oid := e.seed(t, purposeTiny, media.StateUploaded, small)
	big := append(slices.Clone(small), 0)
	if err := e.mem.In.Put(ctx, oid.String(), bytes.NewReader(big), blob.PutOptions{ContentType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	if err := e.pw.Work(ctx, job(oid, 1)); err != nil {
		t.Fatal(err)
	}
	if o := e.object(t, oid); o.State != "rejected" || deref(o.RejectCode) != string(media.RejectTooLarge) {
		t.Fatalf("перезаливка: %+v", o)
	}
}

// Ловушка ревью Task 6: объект сняли во время обработки, входящее удалил media.purge — Get «нет файла»; задача
// перечитывает строку и завершается без изменений (не rejected, не uploaded).
func TestProcessInputGoneAfterTakeDown(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 64, 64))
	e.in.get = func(ctx context.Context, _ string) (io.ReadCloser, blob.Info, error) {
		if _, err := e.pools.Owner.Exec(ctx, `UPDATE media_objects SET state = 'taken_down', ended_at = $2, version = version + 1
			WHERE id = $1`, oid, at); err != nil {
			t.Error(err)
		}
		return nil, blob.Info{}, blob.ErrNotFound
	}
	before := e.object(t, oid).Version
	if err := e.pw.Work(ctx, job(oid, 1)); err != nil {
		t.Fatal(err)
	}
	if o := e.object(t, oid); o.State != "taken_down" || o.Version != before+2 || o.RejectCode != nil {
		t.Fatalf("объект: %+v (версия до %d: захват + снятие)", o, before)
	}
	if n := e.events(t, oid, media.EventObjectRejected); n != 0 {
		t.Fatalf("событий rejected: %d", n)
	}
	// входящего нет, а объект ещё в обработке — обычная ошибка с повтором (не «успех без файла»)
	alive := e.seed(t, mediatest.PurposeImage, media.StateUploaded, nil)
	e.in.get = nil
	if err := e.pw.Work(ctx, job(alive, 1)); err == nil {
		t.Fatal("живой объект без входящего — успех")
	}
	if o := e.object(t, alive); o.State != "uploaded" {
		t.Fatalf("живой объект без входящего: %+v", o)
	}
}

// I-3: строку держит параллельная транзакция — задача ждёт замка и после коммита доводит объект до ready.
func TestProcessWaitsForRowLock(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 64, 64))
	release := lockedTx(t, e.pools.Owner, oid)
	done := make(chan error, 1)
	go func() { done <- e.pw.Work(ctx, job(oid, 1)) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if n := count(t, e.pools.Owner, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query ILIKE '%FOR UPDATE%'`); n > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("задача не ждала замка: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("задача так и не встала на замок")
		}
	}
	if e.in.count("get") != 0 {
		t.Fatal("хранилище тронуто до захвата строки")
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if o := e.object(t, oid); o.State != "ready" {
		t.Fatalf("объект: %+v", o)
	}
}

// I-4: снятие между записью вариантов и публикацией — публикация ставит media.purge по записанным ключам с публичными
// адресами; после него ни в pub, ни в priv нет <id>/, Purger получил адреса. Хук снимает объект изнутри Put: запись
// под замком строки дала бы взаимную блокировку.
func TestTakeDownBetweenWriteAndPublish(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	ins, err := river.NewClient(riverpgxv5.New(e.pools.Owner), queue.InsertConfig(mediatest.Discard()))
	if err != nil {
		t.Fatal(err)
	}
	x, err := intx.New(intx.Deps{Clock: e.clk, Registry: e.reg, River: ins, PublicBaseURL: mediatest.PublicBaseURL,
		Log: mediatest.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 640, 480))
	var took atomic.Bool
	e.pub.put = func(ctx context.Context, _ string, n int) {
		if n != 2 || took.Swap(true) {
			return
		}
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := db.InTx(tctx, e.pools.Owner, func(ctx context.Context, tx pgx.Tx) error {
			return x.TakeDownInTx(ctx, tx, oid, media.Actor{StaffID: uuid.Must(uuid.NewV7()), Role: "moderator", Label: "Модератор"},
				"test.takedown")
		}); err != nil {
			t.Errorf("снятие: %v", err)
		}
	}
	if err := e.pw.Work(ctx, job(oid, 1)); err != nil {
		t.Fatal(err)
	}
	if !took.Load() {
		t.Fatal("снятие не случилось")
	}
	if o := e.object(t, oid); o.State != "taken_down" {
		t.Fatalf("объект: %+v", o)
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_variants WHERE object_id = $1`, oid); n != 0 {
		t.Fatalf("строк вариантов: %d", n)
	}
	if got := keys(t, e.mem.Pub, oid); len(got) != 2 {
		t.Fatalf("до media.purge варианты записаны: %v", got)
	}
	e.runPurges(t, oid)
	if got := append(keys(t, e.mem.Pub, oid), keys(t, e.mem.Priv, oid)...); len(got) != 0 {
		t.Fatalf("после media.purge остались: %v", got)
	}
	if inExists(t, e.mem.In, oid) {
		t.Fatal("входящее осталось")
	}
	urls := e.purger.got()
	slices.Sort(urls)
	want := []string{mediatest.PublicBaseURL + mediatest.VariantKey(oid, "full"), mediatest.PublicBaseURL + mediatest.VariantKey(oid, "thumb")}
	if !slices.Equal(urls, want) {
		t.Fatalf("Purger: %v, ждали %v", urls, want)
	}
}

// R2-I1: второй исполнитель на ready ничего не пишет и не удаляет. При захвате — хранилище не трогается вовсе; при
// публикации (другой опубликовал, пока этот писал те же ключи) — ни media.purge, ни второй вставки вариантов.
func TestSecondExecutorOnReady(t *testing.T) {
	t.Parallel()
	t.Run("при_захвате", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t, envOpt{})
		oid := e.seed(t, mediatest.PurposeImage, media.StateReady, nil, "thumb", "full")
		before := e.object(t, oid)
		if err := e.pw.Work(context.Background(), job(oid, 1)); err != nil {
			t.Fatal(err)
		}
		if n := e.in.total() + e.pub.total() + e.priv.total(); n != 0 {
			t.Fatalf("вызовов хранилища: %d", n)
		}
		if o := e.object(t, oid); o != before {
			t.Fatalf("объект изменён: %+v → %+v", before, o)
		}
		if p := e.purgeJobs(t, oid); len(p) != 0 {
			t.Fatalf("media.purge: %+v", p)
		}
	})
	t.Run("при_публикации", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t, envOpt{})
		ctx := context.Background()
		oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 640, 480))
		var version int64
		e.pub.put = func(ctx context.Context, _ string, n int) {
			if n != 2 {
				return
			}
			// другой исполнитель опубликовал те же ключи
			if err := e.pools.Owner.QueryRow(ctx, `UPDATE media_objects SET state = 'ready', ready_at = $2, width = 640,
				height = 480, version = version + 1 WHERE id = $1 RETURNING version`, oid, at).Scan(&version); err != nil {
				t.Error(err)
			}
			for _, v := range []string{"thumb", "full"} {
				if _, err := e.pools.Owner.Exec(ctx, `INSERT INTO media_variants (object_id, name, blob_key, content_type, width,
					height, bytes) VALUES ($1, $2, $3, 'image/webp', 10, 8, 100)`, oid, v, mediatest.VariantKey(oid, v)); err != nil {
					t.Error(err)
				}
			}
		}
		if err := e.pw.Work(ctx, job(oid, 1)); err != nil {
			t.Fatal(err)
		}
		if p := e.purgeJobs(t, oid); len(p) != 0 {
			t.Fatalf("media.purge на ready: %+v", p)
		}
		if got := keys(t, e.mem.Pub, oid); len(got) != 2 {
			t.Fatalf("файлы готового: %v", got)
		}
		if o := e.object(t, oid); o.State != "ready" || o.Version != version {
			t.Fatalf("объект: %+v, версия другого исполнителя %d", o, version)
		}
		if n := count(t, e.pools.Owner, `SELECT count(*) FROM media_variants WHERE object_id = $1 AND bytes = 100`, oid); n != 2 {
			t.Fatalf("строки вариантов другого исполнителя: %d", n)
		}
		if n := e.events(t, oid, media.EventObjectReady); n != 0 {
			t.Fatalf("событие ready от второго: %d", n)
		}
	})
}

// crash — Work, упавший паникой в Get (процесс исполнителя убит посреди шагов 2–7).
func crash(t *testing.T, e *env, oid uuid.UUID, attempt int) (panicked bool, err error) {
	t.Helper()
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	return false, e.pw.Work(context.Background(), job(oid, attempt))
}

// m2-3: падения считаются захватами, заставшими processing; третий такой захват — rejected(processing_failed) и
// media.purge входящего и всех ключей цели (упавший мог успеть записать варианты).
func TestCrashCountsAttempts(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 64, 64))
	e.in.get = func(context.Context, string) (io.ReadCloser, blob.Info, error) {
		panic("исполнитель убит")
	}
	for i, want := range []int32{0, 1, 2} {
		p, err := crash(t, e, oid, i+1)
		if !p {
			t.Fatalf("захват %d: не упал (%v)", i+1, err)
		}
		if o := e.object(t, oid); o.State != "processing" || o.Attempts != want {
			t.Fatalf("после падения %d: %+v", i+1, o)
		}
	}
	p, err := crash(t, e, oid, 4)
	if p || err != nil {
		t.Fatalf("третий захват после падения: паника %v, %v", p, err)
	}
	if e.in.count("get") != 3 {
		t.Fatalf("Get после третьего падения: %d вызовов", e.in.count("get"))
	}
	o := e.object(t, oid)
	if o.State != "rejected" || deref(o.RejectCode) != string(media.RejectProcessingFailed) || o.Attempts != 3 {
		t.Fatalf("объект: %+v", o)
	}
	if n := e.events(t, oid, media.EventObjectRejected); n != 1 {
		t.Fatalf("событий rejected: %d", n)
	}
	ps := e.purgeJobs(t, oid)
	var all []string
	for _, a := range ps {
		all = append(all, a.Bucket+":"+fmt.Sprint(a.Keys))
	}
	want := []string{"in:[" + oid.String() + "]", "pub:[" + mediatest.VariantKey(oid, "thumb") + " " + mediatest.VariantKey(oid, "full") + "]"}
	slices.Sort(all)
	slices.Sort(want)
	if !slices.Equal(all, want) {
		t.Fatalf("media.purge: %v, ждали %v", all, want)
	}
}

var errBlink = errors.New("хранилище моргнуло")

// m2-3: обычные ошибки хранилища — откат processing → uploaded и ошибка River; падением не считаются. Последняя попытка
// River — rejected(processing_failed).
func TestStoreErrorsDoNotCountAsCrash(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 64, 64))
	e.in.get = func(context.Context, string) (io.ReadCloser, blob.Info, error) { return nil, blob.Info{}, errBlink }
	for attempt := 1; attempt <= 3; attempt++ {
		if err := e.pw.Work(ctx, job(oid, attempt)); !errors.Is(err, errBlink) {
			t.Fatalf("попытка %d: %v", attempt, err)
		}
		if o := e.object(t, oid); o.State != "uploaded" || o.Attempts != 0 {
			t.Fatalf("после попытки %d: %+v", attempt, o)
		}
	}
	if n := e.events(t, oid, media.EventObjectRejected); n != 0 {
		t.Fatalf("событий rejected: %d", n)
	}
	err := e.pw.Work(ctx, job(oid, 10))
	if !errors.Is(err, errBlink) {
		t.Fatalf("последняя попытка: %v", err)
	}
	if o := e.object(t, oid); o.State != "rejected" || deref(o.RejectCode) != string(media.RejectProcessingFailed) || o.Attempts != 0 {
		t.Fatalf("после последней попытки: %+v", o)
	}
	if n := e.events(t, oid, media.EventObjectRejected); n != 1 {
		t.Fatalf("событий rejected: %d", n)
	}
	if len(e.purgeJobs(t, oid)) == 0 {
		t.Fatal("нет media.purge")
	}
}

// cancelCheck — проверка содержимого, отменяющая ctx задачи (остановка воркера посреди обработки).
type cancelCheck struct{ cancel context.CancelFunc }

func (c cancelCheck) Check(context.Context, image.Image) (media.Verdict, error) {
	c.cancel()
	return media.Verdict{Allow: true}, nil
}

// m3-2: отмена ctx посреди обработки — откат processing → uploaded на context.WithoutCancel (на отменённом ctx
// транзакция отката не началась бы, и объект остался бы processing — засчитался бы падением).
func TestRollbackUsesWithoutCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newEnv(t, envOpt{check: cancelCheck{cancel: cancel}})
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 64, 64))
	if err := e.pw.Work(ctx, job(oid, 1)); err == nil {
		t.Fatal("отменённая обработка — успех")
	}
	if o := e.object(t, oid); o.State != "uploaded" || o.Attempts != 0 {
		t.Fatalf("объект: %+v", o)
	}
	if got := keys(t, e.mem.Pub, oid); len(got) != 0 {
		t.Fatalf("после отмены записаны: %v", got)
	}
}

// R2-I1: задача в retryable + повторная вставка (чистка, «завершить») — дубля нет.
func TestUniqueWhileRetryable(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	c, err := river.NewClient(riverpgxv5.New(e.pools.As), queue.InsertConfig(mediatest.Discard()))
	if err != nil {
		t.Fatal(err)
	}
	oid := uuid.Must(uuid.NewV7())
	opts := jobs.ProcessInsertOpts()
	first, err := c.Insert(ctx, jobs.ProcessArgs{V: 1, ObjectID: oid}, &opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pools.Owner.Exec(ctx, `UPDATE river_job SET state = 'retryable', attempt = 1, scheduled_at = now() + interval '1 hour'
		WHERE id = $1`, first.Job.ID); err != nil {
		t.Fatal(err)
	}
	second, err := c.Insert(ctx, jobs.ProcessArgs{V: 1, ObjectID: oid}, &opts)
	if err != nil {
		t.Fatal(err)
	}
	if !second.UniqueSkippedAsDuplicate {
		t.Fatal("вторая задача по объекту в retryable поставлена")
	}
	if n := count(t, e.pools.Owner, `SELECT count(*) FROM river_job WHERE kind = 'media.process'`); n != 1 {
		t.Fatalf("задач: %d", n)
	}
}

// Режим none (m3-3): media.process отменяется, объект и хранилище не трогаются.
func TestProcessNoneCancels(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, nil)
	d := e.deps
	d.Blob = blob.Opened{}
	w, err := jobs.NewProcessWorker(d)
	if err != nil {
		t.Fatal(err)
	}
	err = w.Work(context.Background(), job(oid, 1))
	if !errors.Is(err, &river.JobCancelError{}) || !errors.Is(err, media.ErrUnavailable) {
		t.Fatalf("none: %v", err)
	}
	if o := e.object(t, oid); o.State != "uploaded" {
		t.Fatalf("объект: %+v", o)
	}
}

// V != 1 — обычная ошибка с повтором (задача другого релиза), не отмена; ничего не трогается.
func TestArgsVersion(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	ctx := context.Background()
	oid := e.seed(t, mediatest.PurposeImage, media.StateUploaded, encPNG(t, 16, 16))
	j := job(oid, 1)
	j.Args.V = 2
	if err := e.pw.Work(ctx, j); err == nil || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("process v=2: %v", err)
	}
	if o := e.object(t, oid); o.State != "uploaded" || o.Version != 1 {
		t.Fatalf("объект: %+v", o)
	}
	pj := &river.Job[jobs.PurgeArgs]{JobRow: job(oid, 1).JobRow, Args: jobs.PurgeArgs{V: 2, Bucket: jobs.BucketIn, Keys: []string{oid.String()}}}
	if err := e.gw.Work(ctx, pj); err == nil || errors.Is(err, &river.JobCancelError{}) {
		t.Fatalf("purge v=2: %v", err)
	}
	if !inExists(t, e.mem.In, oid) {
		t.Fatal("purge v=2 удалил файл")
	}
}

// AddWorkers: оба вида; без зависимостей — ошибка сборки.
func TestAddWorkers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOpt{})
	w := river.NewWorkers()
	if err := jobs.AddWorkers(w, e.deps); err != nil {
		t.Fatal(err)
	}
	if got := rivertest.Kinds(w); !slices.Equal(got, []string{"media.process", "media.purge"}) {
		t.Fatalf("виды: %v", got)
	}
	for name, mod := range map[string]func(*jobs.WorkerDeps){
		"pool":   func(d *jobs.WorkerDeps) { d.Pool = nil },
		"clock":  func(d *jobs.WorkerDeps) { d.Clock = nil },
		"reg":    func(d *jobs.WorkerDeps) { d.Registry = nil },
		"check":  func(d *jobs.WorkerDeps) { d.Check = nil },
		"log":    func(d *jobs.WorkerDeps) { d.Log = nil },
		"bucket": func(d *jobs.WorkerDeps) { d.Blob.Buckets.Priv = nil },
		"purger": func(d *jobs.WorkerDeps) { d.Blob.Purger = nil },
	} {
		d := e.deps
		mod(&d)
		if err := jobs.AddWorkers(river.NewWorkers(), d); err == nil {
			t.Errorf("%s: без зависимости собралось", name)
		}
	}
	if p, g := (&jobs.ProcessWorker{}).Timeout(nil), (&jobs.PurgeWorker{}).Timeout(nil); p != 2*time.Minute || g != time.Minute {
		t.Fatalf("сроки: %s %s", p, g)
	}
}
