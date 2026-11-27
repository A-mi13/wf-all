package blobopen_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/blobopen"
	"wf/backend/internal/platform/blob/blobtest"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/testkit/clocktest"
)

var start = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

// devKey — годный ключ подписи fs (32 байта, base64).
var devKey = strings.Repeat("C", 43) + "="

// Режим none (спека media §6.4): хранилища нет, это не ошибка — Available=false, ни бакетов, ни подписи; сброс кэша —
// пустышка (R10).
func TestOpenNone(t *testing.T) {
	o, err := blobopen.Open(context.Background(), config.Blob{Backend: "none", FSRoot: "x", S3Endpoint: "мусор"},
		clocktest.New(start))
	if err != nil {
		t.Fatal(err)
	}
	if o.Available || o.Buckets != (blob.Buckets{}) || o.Presigner != nil || o.Dev != nil || o.PublicBaseURL != "" {
		t.Fatalf("none: %+v", o)
	}
	if _, ok := o.Purger.(blob.NoopPurger); !ok {
		t.Fatalf("none: Purger = %T, ждали blob.NoopPurger", o.Purger)
	}
}

// Режим fs (спека media §6.3, R4, R17): три бакета-каталога под общим корнем, подпись — DevSigner на адресе api (она же
// Presigner), публичные варианты — <адрес api>/_media/pub/.
func TestOpenFS(t *testing.T) {
	root := t.TempDir()
	c := config.Blob{Backend: "fs", FSRoot: root, FSBaseURL: "http://127.0.0.1:8080", DevSignKey: devKey}
	o, err := blobopen.Open(context.Background(), c, clocktest.New(start))
	if err != nil {
		t.Fatal(err)
	}
	if !o.Available || o.Dev == nil || o.Presigner != blob.Presigner(o.Dev) {
		t.Fatalf("fs: Available %v, Dev %v, Presigner %T", o.Available, o.Dev, o.Presigner)
	}
	if o.PublicBaseURL != "http://127.0.0.1:8080/_media/pub/" {
		t.Fatalf("fs: PublicBaseURL = %q", o.PublicBaseURL)
	}
	if _, ok := o.Purger.(blob.NoopPurger); !ok {
		t.Fatalf("fs: Purger = %T, ждали blob.NoopPurger", o.Purger)
	}
	// три разных бакета в каталогах корня: записанное в in не видно в pub и priv
	stores := map[string]blob.Store{"in": o.Buckets.In, "pub": o.Buckets.Pub, "priv": o.Buckets.Priv}
	for name, s := range stores {
		if s == nil {
			t.Fatalf("fs: бакет %s не открыт", name)
		}
		if fi, err := os.Stat(filepath.Join(root, name)); err != nil || !fi.IsDir() {
			t.Fatalf("fs: нет каталога бакета %s: %v", name, err)
		}
	}
	ctx := context.Background()
	if err := o.Buckets.In.Put(ctx, "obj", strings.NewReader("x"), blob.PutOptions{ContentType: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pub", "priv"} {
		if _, err := stores[name].Head(ctx, "obj"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("fs: файл из in виден в %s: %v", name, err)
		}
	}
	// подпись загрузки ведёт на api (FS_BASE_URL), срок — по часам из аргумента
	f, err := o.Presigner.PresignUpload(ctx, blob.UploadSpec{Key: "obj2", ContentType: "image/png", Bytes: 10, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if f.URL != "http://127.0.0.1:8080/_blob/in" || !f.ExpiresAt.Equal(start.Add(time.Minute)) {
		t.Fatalf("fs: форма %s до %s", f.URL, f.ExpiresAt)
	}
}

// Негодная конфигурация — ошибка открытия, а не полуоткрытое хранилище: Open повторяет Validate (без признака
// прод-образа — его проверяет config.LoadBlob) и проверяет адрес публичного медиа s3.
func TestOpenRejectsBadConfig(t *testing.T) {
	clk := clocktest.New(start)
	s3 := config.Blob{Backend: "s3", S3Endpoint: "http://127.0.0.1:1", S3PublicEndpoint: "http://127.0.0.1:1",
		S3Region: "garage", S3BucketIn: "wf-in", S3BucketPub: "wf-pub", S3BucketPriv: "wf-priv", S3AccessKey: "GK",
		S3SecretKey: "s", MediaPublicBaseURL: "https://media.example/"}
	for name, c := range map[string]config.Blob{
		"пустой режим":                 {},
		"minio":                        {Backend: "minio"},
		"fs без ключа":                 {Backend: "fs", FSRoot: t.TempDir(), FSBaseURL: "http://127.0.0.1:8080"},
		"fs с коротким ключом":         {Backend: "fs", FSRoot: t.TempDir(), FSBaseURL: "http://127.0.0.1:8080", DevSignKey: "c2hvcnQ="},
		"s3 без секрета":               func() config.Blob { c := s3; c.S3SecretKey = ""; return c }(),
		"s3 с адресом медиа без схемы": func() config.Blob { c := s3; c.MediaPublicBaseURL = "media.example/"; return c }(),
	} {
		if o, err := blobopen.Open(context.Background(), c, clk); err == nil || o.Available {
			t.Errorf("%s: ждали ошибку, получили %+v", name, o)
		}
	}
	// годная конфигурация s3 открывается без сети (s3.Open в сеть не ходит)
	o, err := blobopen.Open(context.Background(), s3, clk)
	if err != nil || !o.Available || o.PublicBaseURL != "https://media.example/" || o.Dev != nil {
		t.Fatalf("s3 без сети: %v %+v", err, o)
	}
}

// Интеграция с dev-Garage (R13; `bash scripts/s3.sh up`, `eval "$(bash scripts/s3.sh env)"`): без переменных —
// пропуск, при CI=true — падение. Открытое по конфигу хранилище пишет, читает и удаляет в трёх бакетах; подпись —
// S3 (не DevSigner), адрес публичного медиа — из конфига, сброс кэша — пустышка.
func TestOpenS3(t *testing.T) {
	env := blobtest.S3Env(t)
	c := config.Blob{Backend: "s3", S3Endpoint: env.Endpoint, S3PublicEndpoint: env.PublicEndpoint, S3Region: env.Region,
		S3BucketIn: blobtest.S3BucketIn, S3BucketPub: blobtest.S3BucketPub, S3BucketPriv: blobtest.S3BucketPriv,
		S3AccessKey: env.Worker.AccessKey, S3SecretKey: env.Worker.SecretKey, MediaPublicBaseURL: "https://media.example"}
	o, err := blobopen.Open(context.Background(), c, clocktest.New(start))
	if err != nil {
		t.Fatal(err)
	}
	if !o.Available || o.Dev != nil || o.Presigner == nil || o.PublicBaseURL != "https://media.example/" {
		t.Fatalf("s3: %+v", o)
	}
	if _, ok := o.Purger.(blob.NoopPurger); !ok {
		t.Fatalf("s3: Purger = %T, ждали blob.NoopPurger", o.Purger)
	}
	var r [8]byte
	if _, err := rand.Read(r[:]); err != nil {
		t.Fatal(err)
	}
	key := "t" + hex.EncodeToString(r[:]) + "/open"
	ctx := context.Background()
	for name, s := range map[string]blob.Store{"in": o.Buckets.In, "pub": o.Buckets.Pub, "priv": o.Buckets.Priv} {
		if err := s.Put(ctx, key, strings.NewReader(name), blob.PutOptions{ContentType: "text/plain"}); err != nil {
			t.Fatalf("%s: Put: %v", name, err)
		}
		if info, err := s.Head(ctx, key); err != nil || info.Size != int64(len(name)) {
			t.Fatalf("%s: Head: %v %+v", name, err, info)
		}
		if err := s.Delete(ctx, key); err != nil {
			t.Fatalf("%s: Delete: %v", name, err)
		}
	}
}
