package media_test

// Живой путь media на dev-Garage (план media R13, спека §12 «Размер» и «Ключи хранилища (I-9)»): `bash scripts/s3.sh
// up` и `eval "$(bash scripts/s3.sh env)"`; без переменных тесты пропускаются, при CI=true — падают
// (blobtest.S3Env). Каждый процесс — своим ключом по R2: api подписывает загрузку, делает HEAD и подписывает GET;
// admin кладёт прямую загрузку (Stage) и подписывает GET; worker обрабатывает.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	imgpng "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	smithyhttp "github.com/aws/smithy-go/transport/http"
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
	"wf/backend/internal/platform/blob/blobopen"
	"wf/backend/internal/platform/blob/blobtest"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

// purposeS3Tiny — цель с пределом ровно в байты s3PNG(32, 32): граница «Размер» на S3.
const purposeS3Tiny = "e2e_s3_tiny"

// s3WebBase — публичный адрес вариантов: сайт бакета wf-pub (R5, scripts/s3/garage.toml `[s3_web]`), порт —
// WF_S3_WEB_PORT, как у scripts/s3.sh.
func s3WebBase() string {
	port := os.Getenv("WF_S3_WEB_PORT")
	if port == "" {
		port = "13902"
	}
	return "http://" + blobtest.S3BucketPub + ".web.localhost:" + port + "/"
}

// s3Open — хранилище процесса через ту же фабрику, что у бинарников (blobopen.Open), ключом k на часах clk.
func s3Open(t *testing.T, env blobtest.S3, k blobtest.S3Key, clk clock.Clock) blob.Opened {
	t.Helper()
	o, err := blobopen.Open(context.Background(), config.Blob{
		Backend: "s3", S3Endpoint: env.Endpoint, S3PublicEndpoint: env.PublicEndpoint, S3Region: env.Region,
		S3BucketIn: blobtest.S3BucketIn, S3BucketPub: blobtest.S3BucketPub, S3BucketPriv: blobtest.S3BucketPriv,
		S3AccessKey: k.AccessKey, S3SecretKey: k.SecretKey, MediaPublicBaseURL: s3WebBase(),
	}, clk)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// rolePool — пул той же базы под ролью role (права grants.sql уже применены NewPoolsAs).
func rolePool(t *testing.T, owner *pgxpool.Pool, role string) *pgxpool.Pool {
	t.Helper()
	cfg := owner.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func s3PNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := imgpng.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// s3Proc — процесс: пул своей роли, хранилище своим ключом, Service и intx (кроме воркера).
type s3Proc struct {
	pool *pgxpool.Pool
	blob blob.Opened
	svc  media.Service
	tx   *intx.Tx
}

type s3Kit struct {
	owner      *pgxpool.Pool
	reg        *media.Registry
	api, admin s3Proc
	worker     blob.Opened
	pw         *jobs.ProcessWorker
	small      []byte
}

func newS3Proc(t *testing.T, pool *pgxpool.Pool, reg *media.Registry, b blob.Opened, clk clock.Clock) s3Proc {
	t.Helper()
	svc, err := media.New(media.Deps{Pool: pool, Clock: clk, Registry: reg, Blob: b,
		Limiter: ratelimit.NewPG(pool, clk, ratelimit.Players), Log: mediatest.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(mediatest.Discard()))
	if err != nil {
		t.Fatal(err)
	}
	x, err := intx.New(intx.Deps{Clock: clk, Registry: reg, River: ins, PublicBaseURL: b.PublicBaseURL,
		Log: mediatest.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	return s3Proc{pool: pool, blob: b, svc: svc, tx: x}
}

func newS3Kit(t *testing.T, env blobtest.S3) *s3Kit {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api")
	small := s3PNG(t, 32, 32)
	tiny := mediatest.E2EPurposes()[0]
	tiny.Name, tiny.MaxBytes = purposeS3Tiny, int64(len(small))
	reg, err := media.NewRegistry(append(mediatest.E2EPurposes(), tiny)...)
	if err != nil {
		t.Fatal(err)
	}
	k := &s3Kit{owner: pools.Owner, reg: reg, small: small}
	k.api = newS3Proc(t, pools.As, reg, s3Open(t, env, env.API, clock.System), clock.System)
	k.admin = newS3Proc(t, rolePool(t, pools.Owner, "admin"), reg, s3Open(t, env, env.Admin, clock.System), clock.System)
	k.worker = s3Open(t, env, env.Worker, clock.System)
	k.pw, err = jobs.NewProcessWorker(jobs.WorkerDeps{Pool: rolePool(t, pools.Owner, "worker"), Clock: clock.System,
		Registry: reg, Blob: k.worker, Check: mediatest.MarkerCheck, Log: mediatest.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// postTicket — форма POST по билету, как у клиента: поля билета, файл последним полем "file".
func postTicket(t *testing.T, tk media.Ticket, body []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range tk.Fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", "upload")
	if err == nil {
		_, err = fw.Write(body)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), tk.Method, tk.URL, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(msg)
}

// fetch — GET адреса. Хост *.localhost (сайт бакета) соединяется с 127.0.0.1, Host остаётся как в адресе: резолвер
// Windows не обязан знать *.localhost.
func fetch(t *testing.T, raw string) (int, http.Header, []byte) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Host
	if name, port, ok := strings.Cut(u.Host, ":"); ok && strings.HasSuffix(name, ".localhost") {
		u.Host = "127.0.0.1:" + port
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, b
}

func isWebP(b []byte) bool {
	return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
}

// process — прямой вызов исполнителя media.process (задачу вставил AttachInTx или CreateDirectInTx).
func (k *s3Kit) process(t *testing.T, oid uuid.UUID) media.Image {
	t.Helper()
	job := &river.Job[jobs.ProcessArgs]{JobRow: &rivertype.JobRow{ID: 1, Attempt: 1, MaxAttempts: 10, Kind: "media.process"},
		Args: jobs.ProcessArgs{V: 1, ObjectID: oid}}
	if err := k.pw.Work(context.Background(), job); err != nil {
		t.Fatalf("media.process: %v", err)
	}
	img, ok, err := k.api.svc.Image(context.Background(), oid)
	if err != nil || !ok {
		t.Fatalf("Image: %v %v", ok, err)
	}
	return img
}

// upload — путь игрока через api: билет → форма POST в Garage → ConfirmUpload (HEAD) → AttachInTx в транзакции
// владельца. Возвращает билет (живой ещё 15 минут) и владельца.
func (k *s3Kit) upload(t *testing.T, purpose string, data []byte) (media.Ticket, media.OwnerRef, media.Uploader) {
	t.Helper()
	ctx := context.Background()
	owner, by := newOwner(), player()
	tk, err := k.api.svc.NewUpload(ctx, purpose, owner, by, png(int64(len(data))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.worker.Buckets.In.Delete(context.Background(), tk.UploadID.String()) })
	if tk.Method != http.MethodPost || tk.MaxBytes != int64(len(data)) {
		t.Fatalf("билет: %s %d", tk.Method, tk.MaxBytes)
	}
	if status, msg := postTicket(t, tk, data); status != http.StatusNoContent {
		t.Fatalf("загрузка по билету: %d %s", status, msg)
	}
	if err := k.api.svc.ConfirmUpload(ctx, tk.UploadID, owner, by); err != nil {
		t.Fatalf("ConfirmUpload: %v", err)
	}
	err = db.InTx(ctx, k.api.pool, func(ctx context.Context, tx pgx.Tx) error {
		img, err := k.api.tx.AttachInTx(ctx, tx, tk.UploadID, owner, by)
		if err == nil && img.State != media.StateProcessing {
			err = fmt.Errorf("после прикрепления %s", img.State)
		}
		return err
	})
	if err != nil {
		t.Fatalf("AttachInTx: %v", err)
	}
	return tk, owner, by
}

// cleanupVariants — варианты объекта из бакета видимости (тест не оставляет файлов в dev-Garage).
func (k *s3Kit) cleanupVariants(t *testing.T, s blob.Store, oid uuid.UUID) {
	t.Cleanup(func() {
		ctx := context.Background()
		_ = s.List(ctx, oid.String()+"/", func(key string) error { return s.Delete(ctx, key) })
	})
}

// Спека media §12, план R13: весь путь TestMediaE2E на S3-совместимом сервере с ключами процессов по R2 — публичное
// (api: билет и HEAD; worker; сайт бакета отдаёт WebP), закрытое прямой загрузкой (admin: Stage; worker; admin и api
// подписывают GET, после срока — отказ), «Размер» (ровно предел — ready; перезаливка по живому билету между HEAD и Get
// больше предела — хранилище отвергает, обход политики ключом с записью во входящее — too_large).
func TestMediaE2ES3(t *testing.T) {
	t.Parallel()
	env := blobtest.S3Env(t)
	k := newS3Kit(t, env)
	ctx := context.Background()
	data := s3PNG(t, 64, 48)

	t.Run("public", func(t *testing.T) {
		tk, _, _ := k.upload(t, mediatest.PurposeImage, data)
		k.cleanupVariants(t, k.worker.Buckets.Pub, tk.UploadID)
		img := k.process(t, tk.UploadID)
		p, _ := k.reg.Get(mediatest.PurposeImage)
		if img.State != media.StateReady || len(img.Variants) != len(p.Variants) {
			t.Fatalf("объект: %+v", img)
		}
		for _, v := range p.Variants {
			got := img.Variants[v.Name]
			if want := s3WebBase() + tk.UploadID.String() + "/" + v.Name + ".webp"; got.URL != want ||
				got.ContentType != "image/webp" || got.Width != 64 || got.Height != 48 {
				t.Fatalf("вариант %s: %+v, ждали адрес %q", v.Name, got, want)
			}
			status, hdr, b := fetch(t, got.URL)
			if status != http.StatusOK || hdr.Get("Content-Type") != "image/webp" || !isWebP(b) {
				t.Fatalf("сайт бакета %s: %d %v", v.Name, status, hdr)
			}
			if cc := hdr.Get("Cache-Control"); !strings.HasPrefix(cc, "public") {
				t.Fatalf("сайт бакета %s: Cache-Control %q", v.Name, cc)
			}
		}
		if _, err := k.worker.Buckets.In.Head(ctx, tk.UploadID.String()); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("входящее после публикации: %v", err)
		}
		if _, err := k.api.svc.PrivateURL(ctx, tk.UploadID, "full"); !errors.Is(err, media.ErrObjectUnavailable) {
			t.Fatalf("подписанная ссылка на публичное: %v", err)
		}
	})

	t.Run("private_direct", func(t *testing.T) {
		staged, err := k.admin.svc.Stage(ctx, mediatest.PurposeImagePrivate, bytes.NewReader(data), png(int64(len(data))))
		if err != nil {
			t.Fatalf("Stage ключом admin: %v", err)
		}
		oid := staged.ObjectID
		t.Cleanup(func() { _ = k.worker.Buckets.In.Delete(context.Background(), oid.String()) })
		k.cleanupVariants(t, k.worker.Buckets.Priv, oid)
		staff := media.Uploader{StaffID: uuid.Must(uuid.NewV7())}
		if err := db.InTx(ctx, k.admin.pool, func(ctx context.Context, tx pgx.Tx) error {
			_, err := k.admin.tx.CreateDirectInTx(ctx, tx, staged, newOwner(), staff)
			return err
		}); err != nil {
			t.Fatalf("CreateDirectInTx: %v", err)
		}
		img := k.process(t, oid)
		if img.State != media.StateReady || len(img.Variants) == 0 {
			t.Fatalf("закрытый объект: %+v", img)
		}
		for name, v := range img.Variants {
			if v.URL != "" {
				t.Fatalf("закрытый %s с адресом %q", name, v.URL)
			}
		}
		// сайт бакета wf-pub закрытое не отдаёт: ключа там нет
		if status, _, _ := fetch(t, s3WebBase()+oid.String()+"/full.webp"); status != http.StatusNotFound {
			t.Fatalf("закрытое через сайт бакета: %d", status)
		}
		for name, p := range map[string]s3Proc{"admin": k.admin, "api": k.api} {
			link, err := p.svc.PrivateURL(ctx, oid, "full")
			if err != nil {
				t.Fatalf("%s: PrivateURL: %v", name, err)
			}
			if !strings.HasPrefix(link.URL, env.PublicEndpoint+"/") {
				t.Fatalf("%s: ссылка %q не на адрес хранилища", name, link.URL)
			}
			status, hdr, b := fetch(t, link.URL)
			if status != http.StatusOK || hdr.Get("Cache-Control") != "private, no-store" || !isWebP(b) {
				t.Fatalf("%s: закрытое по ссылке: %d %v", name, status, hdr)
			}
		}
		// подписано 10 минут назад со сроком 5 минут — срок вышел (без sleep)
		past := clocktest.New(time.Now().Add(-10 * time.Minute))
		old := newS3Proc(t, k.admin.pool, k.reg, s3Open(t, env, env.Admin, past), past)
		link, err := old.svc.PrivateURL(ctx, oid, "full")
		if err != nil {
			t.Fatal(err)
		}
		// Garage отвечает на истёкшую подпись 400, AWS — 403
		if status, _, _ := fetch(t, link.URL); status != http.StatusBadRequest && status != http.StatusForbidden {
			t.Fatalf("ссылка после срока: %d", status)
		}
	})

	t.Run("size", func(t *testing.T) {
		// ровно предел цели — ready
		tk, _, _ := k.upload(t, purposeS3Tiny, k.small)
		k.cleanupVariants(t, k.worker.Buckets.Pub, tk.UploadID)
		if img := k.process(t, tk.UploadID); img.State != media.StateReady {
			t.Fatalf("ровно MaxBytes: %+v", img)
		}

		tk, _, _ = k.upload(t, purposeS3Tiny, k.small)
		k.cleanupVariants(t, k.worker.Buckets.Pub, tk.UploadID)
		big := append(slices.Clone(k.small), 0)
		// между HEAD («завершить») и Get воркера — перезаливка по живому билету: политика content-length-range
		// отвергает тело другого размера, во входящем остаётся прежнее
		if status, msg := postTicket(t, tk, big); status < 400 || status >= 500 {
			t.Fatalf("перезаливка больше билета принята: %d %s", status, msg)
		}
		if info, err := k.api.blob.Buckets.In.Head(ctx, tk.UploadID.String()); err != nil || info.Size != int64(len(k.small)) {
			t.Fatalf("после отказа перезаливки: %+v %v", info, err)
		}
		// обход политики ключом с записью во входящее (R2: у api и admin она есть) — предел держит LimitReader воркера
		if err := k.api.blob.Buckets.In.Put(ctx, tk.UploadID.String(), bytes.NewReader(big),
			blob.PutOptions{ContentType: "image/png"}); err != nil {
			t.Fatal(err)
		}
		if img := k.process(t, tk.UploadID); img.State != media.StateRejected || img.RejectCode != media.RejectTooLarge {
			t.Fatalf("перезаливка больше предела: %+v", img)
		}
	})
}

// forbidden — отказ хранилища в правах: 403 (AccessDenied; у HEAD тела нет — только статус).
func forbidden(err error) bool {
	var re *smithyhttp.ResponseError
	return errors.As(err, &re) && re.HTTPStatusCode() == http.StatusForbidden
}

// Спека media §6.4 (I-9), R2: каждая операция таблицы прав своим ключом проходит; чужим — отказ там, где R2 его даёт.
// R2 шире §6.4 (принято планом): api и admin читают и пишут во входящее и листают priv — эти операции здесь не
// проверяются ни на успех, ни на отказ.
func TestMediaS3KeyRights(t *testing.T) {
	t.Parallel()
	env := blobtest.S3Env(t)
	ctx := context.Background()
	api := s3Open(t, env, env.API, clock.System)
	admin := s3Open(t, env, env.Admin, clock.System)
	worker := s3Open(t, env, env.Worker, clock.System)
	newKey := func(name string) string {
		key := uuid.Must(uuid.NewV7()).String() + "/" + name
		t.Cleanup(func() {
			for _, s := range []blob.Store{worker.Buckets.In, worker.Buckets.Pub, worker.Buckets.Priv} {
				_ = s.Delete(context.Background(), key)
			}
		})
		return key
	}
	put := func(s blob.Store, key string) error {
		return s.Put(ctx, key, strings.NewReader("webp"), blob.PutOptions{ContentType: "image/webp"})
	}
	exists := func(s blob.Store, key string) bool {
		_, err := s.Head(ctx, key)
		if err != nil && !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("Head ключом worker: %v", err)
		}
		return err == nil
	}

	t.Run("upload_in/api", func(t *testing.T) {
		key := newKey("orig")
		form, err := api.Presigner.PresignUpload(ctx, blob.UploadSpec{Key: key, ContentType: "image/png", Bytes: 4, TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		tk := media.Ticket{Method: http.MethodPost, URL: form.URL, Fields: form.Fields}
		if status, msg := postTicket(t, tk, []byte("abcd")); status != http.StatusNoContent {
			t.Fatalf("подписанная загрузка ключом api: %d %s", status, msg)
		}
		if info, err := api.Buckets.In.Head(ctx, key); err != nil || info.Size != 4 {
			t.Fatalf("HEAD входящего ключом api: %+v %v", info, err)
		}
	})

	t.Run("put_in/admin_worker", func(t *testing.T) {
		for name, b := range map[string]blob.Opened{"admin": admin, "worker": worker} {
			key := newKey("staged")
			if err := put(b.Buckets.In, key); err != nil || !exists(worker.Buckets.In, key) {
				t.Fatalf("%s: Put во входящее: %v", name, err)
			}
		}
	})

	t.Run("worker_all_buckets", func(t *testing.T) {
		for bucket, s := range map[string]blob.Store{"in": worker.Buckets.In, "pub": worker.Buckets.Pub, "priv": worker.Buckets.Priv} {
			key := newKey("full.webp")
			if err := put(s, key); err != nil {
				t.Fatalf("%s: Put: %v", bucket, err)
			}
			rc, _, err := s.Get(ctx, key)
			if err != nil {
				t.Fatalf("%s: Get: %v", bucket, err)
			}
			_ = rc.Close()
			var listed []string
			if err := s.List(ctx, strings.Split(key, "/")[0]+"/", func(k string) error {
				listed = append(listed, k)
				return nil
			}); err != nil || !slices.Equal(listed, []string{key}) {
				t.Fatalf("%s: List: %v %v", bucket, listed, err)
			}
			if err := s.Delete(ctx, key); err != nil || exists(s, key) {
				t.Fatalf("%s: Delete: %v", bucket, err)
			}
		}
	})

	t.Run("presign_get_priv/api_admin", func(t *testing.T) {
		key := newKey("full.webp")
		if err := put(worker.Buckets.Priv, key); err != nil {
			t.Fatal(err)
		}
		for name, b := range map[string]blob.Opened{"api": api, "admin": admin} {
			u, _, err := b.Presigner.PresignGet(ctx, key, time.Minute, "private, no-store")
			if err != nil {
				t.Fatal(err)
			}
			if status, _, body := fetch(t, u); status != http.StatusOK || string(body) != "webp" {
				t.Fatalf("%s: подписанный GET priv: %d", name, status)
			}
		}
	})

	// отказы по R2: у api и admin нет записи и удаления в pub и priv и нет никакого доступа к pub (публичное читается
	// только сайтом бакета)
	t.Run("denied/api_admin", func(t *testing.T) {
		pubKey, privKey := newKey("full.webp"), newKey("full.webp")
		if err := put(worker.Buckets.Pub, pubKey); err != nil {
			t.Fatal(err)
		}
		if err := put(worker.Buckets.Priv, privKey); err != nil {
			t.Fatal(err)
		}
		for name, b := range map[string]blob.Opened{"api": api, "admin": admin} {
			for bucket, c := range map[string]struct {
				s, w blob.Store
				key  string
			}{"pub": {b.Buckets.Pub, worker.Buckets.Pub, pubKey}, "priv": {b.Buckets.Priv, worker.Buckets.Priv, privKey}} {
				if err := put(c.s, newKey("denied.webp")); !forbidden(err) {
					t.Errorf("%s: Put в %s — ждали 403, получили %v", name, bucket, err)
				}
				if err := c.s.Delete(ctx, c.key); !forbidden(err) || !exists(c.w, c.key) {
					t.Errorf("%s: Delete в %s — ждали 403 и файл на месте, получили %v", name, bucket, err)
				}
			}
			if _, err := b.Buckets.Pub.Head(ctx, pubKey); !forbidden(err) {
				t.Errorf("%s: Head в pub — ждали 403, получили %v", name, err)
			}
			if rc, _, err := b.Buckets.Pub.Get(ctx, pubKey); !forbidden(err) {
				if rc != nil {
					_ = rc.Close()
				}
				t.Errorf("%s: Get в pub — ждали 403, получили %v", name, err)
			}
			if err := b.Buckets.Pub.List(ctx, "", func(string) error { return nil }); !forbidden(err) {
				t.Errorf("%s: List в pub — ждали 403, получили %v", name, err)
			}
		}
	})
}
