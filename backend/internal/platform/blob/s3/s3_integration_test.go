package s3_test

// Интеграция с dev-Garage (R13): `bash scripts/s3.sh up` и `eval "$(bash scripts/s3.sh env)"`; без переменных тесты
// пропускаются, при CI=true — падают (blobtest.S3Env).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/blobtest"
	"wf/backend/internal/platform/blob/s3"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/testkit/clocktest"
)

func openAs(t *testing.T, env blobtest.S3, k blobtest.S3Key, clk clock.Clock) (blob.Buckets, blob.Presigner) {
	t.Helper()
	b, p, err := s3.Open(context.Background(), s3.Config{
		Endpoint:       env.Endpoint,
		PublicEndpoint: env.PublicEndpoint,
		Region:         env.Region,
		BucketIn:       blobtest.S3BucketIn,
		BucketPub:      blobtest.S3BucketPub,
		BucketPriv:     blobtest.S3BucketPriv,
		AccessKey:      k.AccessKey,
		SecretKey:      k.SecretKey,
	}, clk)
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}

func uniqueKey(t *testing.T, name string) string {
	t.Helper()
	var r [8]byte
	if _, err := rand.Read(r[:]); err != nil {
		t.Fatal(err)
	}
	return "t" + hex.EncodeToString(r[:]) + "/" + name
}

func TestS3StoreContract(t *testing.T) {
	env := blobtest.S3Env(t)
	b, _ := openAs(t, env, env.Worker, clock.System)
	blobtest.Contract(t, b)
}

// postForm — загрузка формой, как у клиента: поля билета, файл последним полем "file". contentType != "" подменяет
// поле Content-Type формы.
func postForm(t *testing.T, f blob.UploadForm, body []byte, contentType string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range f.Fields {
		if contentType != "" && strings.EqualFold(k, blob.FieldContentType) {
			v = contentType
		}
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", "upload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.URL, &buf)
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

// TestS3UploadExactSize — R3: подписанный POST принимает ровно N байт заявленного типа до срока.
func TestS3UploadExactSize(t *testing.T) {
	env := blobtest.S3Env(t)
	const n = 1000
	b, p := openAs(t, env, env.API, clock.System)
	worker, _ := openAs(t, env, env.Worker, clock.System)
	ctx := context.Background()

	sign := func(t *testing.T, p blob.Presigner, ttl time.Duration) (string, blob.UploadForm) {
		t.Helper()
		key := uniqueKey(t, "orig")
		t.Cleanup(func() { _ = worker.In.Delete(context.Background(), key) })
		f, err := p.PresignUpload(ctx, blob.UploadSpec{Key: key, ContentType: "image/jpeg", Bytes: n, TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		return key, f
	}
	rejected := func(t *testing.T, key string, status int, msg string) {
		t.Helper()
		if status < 400 || status >= 500 {
			t.Fatalf("хранилище приняло: %d %s", status, msg)
		}
		if _, err := b.In.Head(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("после отказа объект есть: %v", err)
		}
	}

	t.Run("exact", func(t *testing.T) {
		key, f := sign(t, p, time.Minute)
		if status, msg := postForm(t, f, bytes.Repeat([]byte{'x'}, n), ""); status != http.StatusNoContent {
			t.Fatalf("ровно N байт: %d %s", status, msg)
		}
		info, err := b.In.Head(ctx, key)
		if err != nil || info.Size != n || info.ContentType != "image/jpeg" {
			t.Fatalf("Head: %+v %v", info, err)
		}
	})
	for name, size := range map[string]int{"n_plus_1": n + 1, "n_minus_1": n - 1} {
		t.Run(name, func(t *testing.T) {
			key, f := sign(t, p, time.Minute)
			status, msg := postForm(t, f, bytes.Repeat([]byte{'x'}, size), "")
			rejected(t, key, status, msg)
		})
	}
	t.Run("other_type", func(t *testing.T) {
		key, f := sign(t, p, time.Minute)
		status, msg := postForm(t, f, bytes.Repeat([]byte{'x'}, n), "text/html")
		rejected(t, key, status, msg)
	})
	t.Run("expired", func(t *testing.T) {
		// подписано минуту назад со сроком 1 с — срок давно вышел, без sleep
		_, past := openAs(t, env, env.API, clocktest.New(time.Now().Add(-time.Minute)))
		key, f := sign(t, past, time.Second)
		status, msg := postForm(t, f, bytes.Repeat([]byte{'x'}, n), "")
		rejected(t, key, status, msg)
	})
}

// TestS3PutBodies — длина тела для подписи: поток без Seek читается в память, ReadSeeker идёт с текущей позиции.
func TestS3PutBodies(t *testing.T) {
	env := blobtest.S3Env(t)
	w, _ := openAs(t, env, env.Worker, clock.System)
	ctx := context.Background()
	seeked := strings.NewReader("skip-body")
	if _, err := seeked.Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]io.Reader{
		"stream": io.MultiReader(strings.NewReader("bo"), strings.NewReader("dy")),
		"seeked": seeked,
	} {
		key := uniqueKey(t, name)
		if err := w.Priv.Put(ctx, key, r, blob.PutOptions{ContentType: "text/plain"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rc, info, err := w.Priv.Get(ctx, key)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		_ = w.Priv.Delete(ctx, key)
		if string(got) != "body" || info.Size != 4 {
			t.Fatalf("%s: %q %+v", name, got, info)
		}
	}
}

// TestS3PresignGetServes — ссылка, подписанная на PublicEndpoint со временем clk, открывается и отдаёт подписанный
// Cache-Control; ссылка, срок которой истёк, — отказ. Действующая ссылка — на минуту: подпись усекает время до
// секунды, и короткий срок под нагрузкой CI истекал бы до запроса (ревью Task 3, m2); истёкшая — подписана минуту
// назад на 2 с.
func TestS3PresignGetServes(t *testing.T) {
	env := blobtest.S3Env(t)
	w, _ := openAs(t, env, env.Worker, clock.System)
	_, p := openAs(t, env, env.API, clock.System)
	_, past := openAs(t, env, env.API, clocktest.New(time.Now().Add(-time.Minute)))
	ctx := context.Background()
	key := uniqueKey(t, "full.webp")
	if err := w.Priv.Put(ctx, key, strings.NewReader("RIFF"), blob.PutOptions{ContentType: "image/webp"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Priv.Delete(context.Background(), key) })
	get := func(p blob.Presigner, ttl time.Duration) (int, string) {
		u, _, err := p.PresignGet(ctx, key, ttl, "private, max-age=120")
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Cache-Control")
	}
	if status, cc := get(p, time.Minute); status != http.StatusOK || cc != "private, max-age=120" {
		t.Fatalf("подписанный GET: %d %q", status, cc)
	}
	// Garage отвечает на истёкшую подпись 400, AWS — 403
	if status, _ := get(past, 2*time.Second); status != http.StatusBadRequest && status != http.StatusForbidden {
		t.Fatalf("GET после срока: %d", status)
	}
}

func accessDenied(err error) bool {
	var ae smithy.APIError
	return errors.As(err, &ae) && ae.ErrorCode() == "AccessDenied"
}

// TestS3KeyRights — R2: api и admin не пишут в pub и priv, worker пишет и удаляет во всех трёх бакетах.
func TestS3KeyRights(t *testing.T) {
	env := blobtest.S3Env(t)
	ctx := context.Background()
	put := func(s blob.Store, key string) error {
		return s.Put(ctx, key, strings.NewReader("x"), blob.PutOptions{ContentType: "text/plain"})
	}
	for name, k := range map[string]blobtest.S3Key{"api": env.API, "admin": env.Admin} {
		b, _ := openAs(t, env, k, clock.System)
		for bucket, s := range map[string]blob.Store{"pub": b.Pub, "priv": b.Priv} {
			if err := put(s, uniqueKey(t, "denied")); !accessDenied(err) {
				t.Errorf("%s: Put в %s — ждали AccessDenied, получили %v", name, bucket, err)
			}
		}
	}
	w, _ := openAs(t, env, env.Worker, clock.System)
	for bucket, s := range map[string]blob.Store{"in": w.In, "pub": w.Pub, "priv": w.Priv} {
		key := uniqueKey(t, "worker")
		if err := put(s, key); err != nil {
			t.Fatalf("worker: Put в %s: %v", bucket, err)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatalf("worker: Delete в %s: %v", bucket, err)
		}
		if _, err := s.Head(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("worker: после Delete в %s: %v", bucket, err)
		}
	}
}
