package httpdev_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/media/httpdev"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/blobopen"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/testkit/clocktest"
)

var at = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

const baseURL = "http://127.0.0.1:8080"

var devKey = base64.StdEncoding.EncodeToString([]byte("httpdev-test-sign-key-0123456789abcdef"))

// openMode — хранилище процесса в режиме mode через ту же фабрику, что у бинарников (blobopen.Open).
func openMode(t *testing.T, mode string, clk clock.Clock) blob.Opened {
	t.Helper()
	c := config.Blob{Backend: mode}
	switch mode {
	case "fs":
		c.FSRoot, c.FSBaseURL, c.DevSignKey = t.TempDir(), baseURL, devKey
	case "s3":
		c.S3Endpoint, c.S3PublicEndpoint, c.S3Region = "http://storage:3900", "https://storage.wf.test", "garage"
		c.S3BucketIn, c.S3BucketPub, c.S3BucketPriv = "wf-in", "wf-pub", "wf-priv"
		c.S3AccessKey, c.S3SecretKey, c.MediaPublicBaseURL = "GKtest", "secret", "https://media.wf.test/"
	}
	o, err := blobopen.Open(context.Background(), c, clk)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func newHandler(t *testing.T, o blob.Opened) *httpdev.Handler {
	t.Helper()
	reg, err := mediatest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return httpdev.New(o, reg, mediatest.Discard())
}

// mount — как public.NewHandler: dev-пути есть, только если httpdev.New их отдал.
func mount(h *httpdev.Handler) http.Handler {
	if h == nil {
		return http.NotFoundHandler()
	}
	return h
}

type part struct{ name, value string }

// form — multipart в порядке parts; поле с именем file — файл.
func form(t *testing.T, parts ...part) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		var err error
		if p.name == "file" {
			fw, ferr := w.CreateFormFile("file", "photo.png")
			if ferr == nil {
				_, err = fw.Write([]byte(p.value))
			} else {
				err = ferr
			}
		} else {
			err = w.WriteField(p.name, p.value)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

// ticketParts — поля подписанной формы и файл последним.
func ticketParts(f blob.UploadForm, file string) []part {
	return []part{
		{blob.FieldKey, f.Fields[blob.FieldKey]},
		{blob.FieldContentType, f.Fields[blob.FieldContentType]},
		{blob.FieldPolicy, f.Fields[blob.FieldPolicy]},
		{blob.FieldSignature, f.Fields[blob.FieldSignature]},
		{"file", file},
	}
}

func post(h http.Handler, body *bytes.Buffer, ct string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/_blob/in", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func presign(t *testing.T, o blob.Opened, key, ct string, n int64) blob.UploadForm {
	t.Helper()
	f, err := o.Presigner.PresignUpload(context.Background(), blob.UploadSpec{Key: key, ContentType: ct, Bytes: n, TTL: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

const objKey = "019a0000-0000-7000-8000-000000000001"

// спека §6.3: при s3 и none путей /_blob и /_media нет (404); при fs — работают.
func TestDevPathsOnlyInFS(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"fs", "s3", "none"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			clk := clocktest.New(at)
			o := openMode(t, mode, clk)
			h := mount(newHandler(t, o))
			var body *bytes.Buffer
			var ct string
			if mode == "fs" {
				body, ct = form(t, ticketParts(presign(t, o, objKey, "image/png", 3), "png")...)
			} else {
				body, ct = form(t, part{"file", "png"})
			}
			up := post(h, body, ct).Code
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/_media/pub/x/thumb.webp", nil))
			pub := rec.Code
			if mode == "fs" {
				if up != http.StatusNoContent || pub != http.StatusNotFound {
					t.Fatalf("fs: загрузка %d, чтение отсутствующего %d", up, pub)
				}
				if _, err := o.Buckets.In.Head(context.Background(), objKey); err != nil {
					t.Fatalf("fs: файла во входящем нет: %v", err)
				}
				return
			}
			if up != http.StatusNotFound || pub != http.StatusNotFound {
				t.Fatalf("%s: dev-пути отвечают %d / %d, ждали 404", mode, up, pub)
			}
		})
	}
}

// R4: форма повторяет S3 POST — подпись проверяется до записи; размер ±1, чужой тип, чужая подпись, истёкший срок —
// 403; файл не последним, поле после файла, без файла, дубль поля, обрыв тела — 400. Отказ ничего не кладёт во входящее.
func TestDevUploadForm(t *testing.T) {
	t.Parallel()
	clk := clocktest.New(at)
	o := openMode(t, "fs", clk)
	h := newHandler(t, o)
	in := o.Buckets.In
	f := presign(t, o, objKey, "image/png", 4)
	good := ticketParts(f, "abcd")

	with := func(name, value string) []part {
		out := make([]part, 0, len(good))
		for _, p := range good {
			if p.name == name {
				p.value = value
			}
			out = append(out, p)
		}
		return out
	}
	forged := presign(t, o, objKey, "image/png", 4)
	forged.Fields[blob.FieldSignature] = strings.Repeat("0", 64)

	cases := []struct {
		name  string
		parts []part
		want  int
	}{
		{"меньше на 1", with("file", "abc"), http.StatusForbidden},
		{"больше на 1", with("file", "abcde"), http.StatusForbidden},
		{"чужой тип в поле формы", with(blob.FieldContentType, "image/jpeg"), http.StatusForbidden},
		{"чужой ключ", with(blob.FieldKey, "019a0000-0000-7000-8000-000000000002"), http.StatusForbidden},
		{"чужая подпись", ticketParts(forged, "abcd"), http.StatusForbidden},
		{"лишнее поле", append([]part{{"acl", "public-read"}}, good...), http.StatusForbidden},
		{"файл не последним", append(append([]part{}, good[4], good[0], good[1]), good[2:4]...), http.StatusBadRequest},
		{"поле после файла", append(append([]part{}, good...), part{"x-extra", "1"}), http.StatusBadRequest},
		{"без файла", good[:4], http.StatusBadRequest},
		{"дубль поля", append([]part{good[0]}, good...), http.StatusBadRequest},
	}
	for _, c := range cases {
		body, ct := form(t, c.parts...)
		if rec := post(h, body, ct); rec.Code != c.want {
			t.Errorf("%s: %d, ждали %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
		if _, err := in.Head(context.Background(), objKey); err == nil {
			t.Fatalf("%s: отказ положил файл во входящее", c.name)
		}
	}
	// обрыв тела посреди файла
	body, ct := form(t, good...)
	cut := bytes.NewBuffer(body.Bytes()[:body.Len()-20])
	if rec := post(h, cut, ct); rec.Code != http.StatusBadRequest {
		t.Errorf("обрыв тела: %d", rec.Code)
	}
	// не multipart
	if rec := post(h, bytes.NewBufferString("{}"), "application/json"); rec.Code != http.StatusBadRequest {
		t.Errorf("не multipart: %d", rec.Code)
	}
	// срок: ровно в момент истечения — уже 403
	clk.Set(f.ExpiresAt)
	body, ct = form(t, good...)
	if rec := post(h, body, ct); rec.Code != http.StatusForbidden {
		t.Errorf("ровно в срок: %d", rec.Code)
	}
	if _, err := in.Head(context.Background(), objKey); err == nil {
		t.Fatal("истёкшая форма положила файл")
	}
	// за секунду до срока — 204, файл во входящем с типом из поля формы
	clk.Set(f.ExpiresAt.Add(-time.Second))
	body, ct = form(t, good...)
	if rec := post(h, body, ct); rec.Code != http.StatusNoContent {
		t.Fatalf("верная форма: %d (%s)", rec.Code, rec.Body)
	}
	info, err := in.Head(context.Background(), objKey)
	if err != nil || info.Size != 4 || info.ContentType != "image/png" {
		t.Fatalf("входящее: %+v %v", info, err)
	}
	// другой метод
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/_blob/in", strings.NewReader("abcd")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: %d", rec.Code)
	}
}

// CORS загрузки — только origin dev-фронтов (loopback): подпись — единственная защита, cookie нет.
func TestDevUploadCORS(t *testing.T) {
	t.Parallel()
	h := newHandler(t, openMode(t, "fs", clocktest.New(at)))
	for origin, allowed := range map[string]bool{
		"http://localhost:3000": true, "http://127.0.0.1:5173": true, "http://[::1]:3000": true,
		"https://evil.example": false, "http://localhost.evil.example": false, "null": false,
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/_blob/in", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get("Access-Control-Allow-Origin")
		if allowed && (got != origin || rec.Code != http.StatusNoContent) {
			t.Errorf("%s: %d, Allow-Origin %q", origin, rec.Code, got)
		}
		if !allowed && got != "" {
			t.Errorf("%s: чужой origin разрешён", origin)
		}
	}
}

func put(t *testing.T, s blob.Store, key, ct string, data string) {
	t.Helper()
	if err := s.Put(context.Background(), key, strings.NewReader(data), blob.PutOptions{ContentType: ct}); err != nil {
		t.Fatal(err)
	}
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	return rec
}

// §3.4, §3.5: публичное — тип варианта, nosniff, public на сутки; входящее и обход каталога — 404; закрытое — только по
// подписи (без неё, чужая, истёкшая — 403), ответ private, no-store.
func TestDevMediaHeaders(t *testing.T) {
	t.Parallel()
	clk := clocktest.New(at)
	o := openMode(t, "fs", clk)
	h := newHandler(t, o)
	put(t, o.Buckets.In, objKey, "image/png", "raw")
	put(t, o.Buckets.Pub, objKey+"/thumb.webp", "image/webp", "RIFFwebp")
	put(t, o.Buckets.Priv, objKey+"/full.webp", "image/webp", "RIFFpriv")

	rec := get(h, "/_media/pub/"+objKey+"/thumb.webp")
	if rec.Code != http.StatusOK || rec.Body.String() != "RIFFwebp" {
		t.Fatalf("pub: %d %q", rec.Code, rec.Body)
	}
	for k, want := range map[string]string{"Content-Type": "image/webp", "X-Content-Type-Options": "nosniff",
		"Cache-Control": "public, max-age=86400"} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("pub %s = %q, ждали %q", k, got, want)
		}
	}
	for _, p := range []string{"/_media/pub/" + objKey, "/_media/pub/../in/" + objKey, "/_media/pub/..%2Fin%2F" + objKey,
		"/_media/pub/" + objKey + "/full.webp", "/_media/pub/", "/_media/x/" + objKey} {
		if rec := get(h, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, ждали 404", p, rec.Code)
		}
	}
	link, _, err := o.Presigner.PresignGet(context.Background(), objKey+"/full.webp", 5*time.Minute, "private, no-store")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if rec := get(h, u.Path); rec.Code != http.StatusForbidden {
		t.Errorf("priv без подписи: %d", rec.Code)
	}
	q := u.Query()
	q.Set("sig", strings.Repeat("0", 64))
	if rec := get(h, u.Path+"?"+q.Encode()); rec.Code != http.StatusForbidden {
		t.Errorf("priv с чужой подписью: %d", rec.Code)
	}
	rec = get(h, u.RequestURI())
	if rec.Code != http.StatusOK || rec.Body.String() != "RIFFpriv" {
		t.Fatalf("priv: %d %q", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("priv Cache-Control = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("priv nosniff = %q", got)
	}
	// ссылка на закрытое не открывает публичный бакет и наоборот
	if rec := get(h, "/_media/priv/"+objKey+"/thumb.webp?"+u.RawQuery); rec.Code != http.StatusForbidden {
		t.Errorf("подпись чужого ключа: %d", rec.Code)
	}
	clk.Advance(5 * time.Minute)
	if rec := get(h, u.RequestURI()); rec.Code != http.StatusForbidden {
		t.Errorf("priv ровно в срок: %d", rec.Code)
	}
	// изменение — не GET/HEAD
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/_media/pub/"+objKey+"/thumb.webp", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE pub: %d", rec.Code)
	}
}

// R16: предел тела POST /_blob/in — максимум реестра и запас на поля формы.
func TestDevUploadLimit(t *testing.T) {
	t.Parallel()
	h := newHandler(t, openMode(t, "fs", clocktest.New(at)))
	if got, want := h.UploadLimit(), int64(mediatest.MaxBytes+64<<10); got != want {
		t.Fatalf("UploadLimit = %d, ждали %d", got, want)
	}
}
