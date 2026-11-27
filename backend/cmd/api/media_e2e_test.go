//go:build wf_e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/cmd/internal/mediapurposes"
	"wf/backend/internal/media"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/blob"
)

// player — токен доступа нового игрока.
func (m *mediaAPI) player(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	m.users.Store(sid, uid)
	tok, _, err := m.iss.Access(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	return uid, tok
}

// do — запрос к api; tok — bearer (пусто — без входа), hdr — пары заголовков.
func (m *mediaAPI) do(t *testing.T, method, target, tok string, body io.Reader, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, body)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	m.h.ServeHTTP(rec, req)
	return rec
}

// doJSON — запрос с телом JSON.
func (m *mediaAPI) doJSON(t *testing.T, method, target, tok string, v any, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return m.do(t, method, target, tok, bytes.NewReader(raw), append([]string{"Content-Type", "application/json"}, hdr...)...)
}

// workerPool — пул той же базы под ролью worker (исполнитель media.process).
func (m *mediaAPI) workerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := m.pools.Owner.Config().Copy()
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

// pathOf — путь и запрос адреса (хост — внешний адрес api fs).
func pathOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme+"://"+u.Host != fsBaseURL {
		t.Fatalf("адрес %q не на api (%v)", raw, err)
	}
	return u.RequestURI()
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type ticketResp struct {
	UploadID uuid.UUID         `json:"upload_id"`
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Fields   map[string]string `json:"fields"`
	MaxBytes int64             `json:"max_bytes"`
}

type imageResp struct {
	ID         uuid.UUID `json:"id"`
	State      string    `json:"state"`
	RejectCode *string   `json:"reject_code"`
	Variants   map[string]struct {
		URL         *string `json:"url"`
		Width       int     `json:"width"`
		Height      int     `json:"height"`
		ContentType string  `json:"content_type"`
	} `json:"variants"`
}

func decodeTo[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("ответ %d, ждали %d: %s", rec.Code, status, rec.Body)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("тело %q: %v", rec.Body, err)
	}
	return v
}

// flow — один объект через тестовые ручки: «начать» (с Idempotency-Key и повтором после «обрыва»), форма POST по
// билету, «завершить», прямой вызов исполнителя media.process. bodies — все тела ответов тестовых ручек по пути.
type flow struct {
	oid    uuid.UUID
	owner  uuid.UUID
	tok    string
	bodies []string
}

func (m *mediaAPI) run(t *testing.T, purpose string, data []byte) flow {
	t.Helper()
	_, tok := m.player(t)
	f := flow{owner: uuid.Must(uuid.NewV7()), tok: tok}
	keep := func(rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		f.bodies = append(f.bodies, rec.Body.String())
		return rec
	}
	start := map[string]any{"purpose": purpose, "owner_id": f.owner, "content_type": "image/png", "bytes": len(data)}
	key := "start-" + uuid.NewString()
	first := keep(m.doJSON(t, http.MethodPost, "/_e2e/media/uploads", tok, start, "Idempotency-Key", key))
	tk := decodeTo[ticketResp](t, first, http.StatusCreated)
	if cc := first.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("билет: Cache-Control %q", cc)
	}
	// повтор с тем же ключом после обрыва — тот же билет из сохранённого ответа (одна закоммиченная tx у NewUpload)
	again := keep(m.doJSON(t, http.MethodPost, "/_e2e/media/uploads", tok, start, "Idempotency-Key", key))
	if tk2 := decodeTo[ticketResp](t, again, http.StatusCreated); tk2.UploadID != tk.UploadID ||
		again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор «начать»: %s %v", tk2.UploadID, again.Header())
	}
	if tk.Method != http.MethodPost || tk.MaxBytes != int64(len(data)) {
		t.Fatalf("билет: %+v", tk)
	}
	// форма POST по билету: поля билета, файл последним (как у S3)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, name := range []string{blob.FieldKey, blob.FieldContentType, blob.FieldPolicy, blob.FieldSignature} {
		if err := mw.WriteField(name, tk.Fields[name]); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("file", "photo.png")
	if err == nil {
		_, err = fw.Write(data)
	}
	if err == nil {
		err = mw.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if rec := m.do(t, http.MethodPost, pathOf(t, tk.URL), "", &body, "Content-Type", mw.FormDataContentType()); rec.Code != http.StatusNoContent {
		t.Fatalf("форма загрузки: %d %s", rec.Code, rec.Body)
	}
	done := keep(m.doJSON(t, http.MethodPost, "/_e2e/media/objects", tok, map[string]any{"upload_id": tk.UploadID, "owner_id": f.owner},
		"Idempotency-Key", "done-"+uuid.NewString()))
	if img := decodeTo[imageResp](t, done, http.StatusOK); img.ID != tk.UploadID || img.State != "processing" || len(img.Variants) != 0 {
		t.Fatalf("«завершить»: %s", done.Body)
	}
	f.oid = tk.UploadID
	keep(m.do(t, http.MethodGet, "/_e2e/media/objects/"+f.oid.String(), tok, nil))
	pw, err := jobs.NewProcessWorker(jobs.WorkerDeps{Pool: m.workerPool(t), Clock: m.clk, Registry: m.kit.reg, Blob: m.kit.blob,
		Check: mediapurposes.Check(), Log: mediatest.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	job := &river.Job[jobs.ProcessArgs]{JobRow: &rivertype.JobRow{ID: 1, Attempt: 1, MaxAttempts: 10, Kind: "media.process"},
		Args: jobs.ProcessArgs{V: 1, ObjectID: f.oid}}
	if err := pw.Work(context.Background(), job); err != nil {
		t.Fatalf("media.process: %v", err)
	}
	return f
}

// Спека media §12a, план Task 11: весь путь на FS в одном процессе — «начать» → форма POST → «завершить» → исполнитель
// media.process → объект ready со всеми вариантами цели → публичный адрес отдаёт WebP; закрытая цель — MediaImage без
// адресов, ссылка private работает, после срока — 403; «начать» без входа — 401, в режиме none — 503 media.unavailable.
func TestMediaE2E(t *testing.T) {
	t.Parallel()
	m := newMediaAPI(t, "fs")
	data := testPNG(t, 64, 48)

	t.Run("public", func(t *testing.T) {
		f := m.run(t, mediatest.PurposeImage, data)
		img := decodeTo[imageResp](t, m.do(t, http.MethodGet, "/_e2e/media/objects/"+f.oid.String(), f.tok, nil), http.StatusOK)
		p, _ := m.kit.reg.Get(mediatest.PurposeImage)
		if img.State != "ready" || len(img.Variants) != len(p.Variants) {
			t.Fatalf("объект: %+v", img)
		}
		for _, v := range p.Variants {
			got, ok := img.Variants[v.Name]
			if !ok || got.URL == nil || got.ContentType != "image/webp" || got.Width != 64 || got.Height != 48 {
				t.Fatalf("вариант %s: %+v", v.Name, got)
			}
			if want := m.kit.blob.PublicBaseURL + f.oid.String() + "/" + v.Name + ".webp"; *got.URL != want {
				t.Fatalf("адрес %s = %q, ждали %q", v.Name, *got.URL, want)
			}
			rec := m.do(t, http.MethodGet, pathOf(t, *got.URL), "", nil)
			b := rec.Body.Bytes()
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/webp" || len(b) < 12 ||
				string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
				t.Fatalf("публичный %s: %d %v", v.Name, rec.Code, rec.Header())
			}
		}
		// публичному подписанная ссылка не выдаётся
		if rec := m.do(t, http.MethodGet, "/_e2e/media/objects/"+f.oid.String()+"/private?variant=full", f.tok, nil); rec.Code != http.StatusConflict ||
			!strings.Contains(rec.Body.String(), media.CodeObjectUnavailable) {
			t.Fatalf("private у публичного: %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("private", func(t *testing.T) {
		f := m.run(t, mediatest.PurposeImagePrivate, data)
		rec := m.do(t, http.MethodGet, "/_e2e/media/objects/"+f.oid.String(), f.tok, nil)
		img := decodeTo[imageResp](t, rec, http.StatusOK)
		if img.State != "ready" || len(img.Variants) == 0 {
			t.Fatalf("закрытый объект: %s", rec.Body)
		}
		for name, v := range img.Variants {
			if v.URL != nil {
				t.Fatalf("закрытый %s с адресом %q", name, *v.URL)
			}
		}
		link := m.do(t, http.MethodGet, "/_e2e/media/objects/"+f.oid.String()+"/private?variant=full", f.tok, nil)
		if link.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("ссылка: Cache-Control %q", link.Header().Get("Cache-Control"))
		}
		l := decodeTo[struct {
			URL       string    `json:"url"`
			ExpiresAt time.Time `json:"expires_at"`
		}](t, link, http.StatusOK)
		got := m.do(t, http.MethodGet, pathOf(t, l.URL), "", nil)
		if got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("закрытое по ссылке: %d %v", got.Code, got.Header())
		}
		m.clk.Set(l.ExpiresAt)
		if got := m.do(t, http.MethodGet, pathOf(t, l.URL), "", nil); got.Code != http.StatusForbidden {
			t.Fatalf("ссылка ровно в срок: %d", got.Code)
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		rec := m.doJSON(t, http.MethodPost, "/_e2e/media/uploads", "", map[string]any{"purpose": mediatest.PurposeImage,
			"owner_id": uuid.New(), "content_type": "image/png", "bytes": 10})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("без входа: %d", rec.Code)
		}
	})

	t.Run("none", func(t *testing.T) {
		n := newMediaAPI(t, "none")
		_, tok := n.player(t)
		rec := n.doJSON(t, http.MethodPost, "/_e2e/media/uploads", tok, map[string]any{"purpose": mediatest.PurposeImage,
			"owner_id": uuid.New(), "content_type": "image/png", "bytes": 10})
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"`+media.CodeUnavailable+`"`) {
			t.Fatalf("none: %d %s", rec.Code, rec.Body)
		}
		if rec := n.do(t, http.MethodPost, "/_blob/in", "", strings.NewReader("x")); rec.Code != http.StatusNotFound {
			t.Fatalf("none: /_blob/in %d", rec.Code)
		}
	})
}

// Спека media §12 «Закрытое»: ни один ответ тестовых ручек и ни один результат Service для закрытого объекта не несёт
// публичного адреса (MEDIA_PUBLIC_BASE_URL) — ни на каком шаге: билет, «завершить», чтение в обработке и готовым, ссылка.
func TestNoPublicURLForPrivate(t *testing.T) {
	t.Parallel()
	m := newMediaAPI(t, "fs")
	f := m.run(t, mediatest.PurposeImagePrivate, testPNG(t, 40, 30))
	ctx := context.Background()
	bodies := append([]string{}, f.bodies...)
	for _, target := range []string{"/_e2e/media/objects/" + f.oid.String(),
		"/_e2e/media/objects/" + f.oid.String() + "/private?variant=full",
		"/_e2e/media/objects/" + f.oid.String() + "/private?variant=thumb"} {
		rec := m.do(t, http.MethodGet, target, f.tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body)
		}
		bodies = append(bodies, rec.Body.String())
	}
	img, ok, err := m.kit.svc.Image(ctx, f.oid)
	if err != nil || !ok || img.State != media.StateReady || len(img.Variants) == 0 {
		t.Fatalf("Image: %+v %v %v", img, ok, err)
	}
	imgs, err := m.kit.svc.Images(ctx, []uuid.UUID{f.oid})
	if err != nil {
		t.Fatal(err)
	}
	link, err := m.kit.svc.PrivateURL(ctx, f.oid, "full")
	if err != nil {
		t.Fatal(err)
	}
	bodies = append(bodies, fmt.Sprintf("%+v", img), fmt.Sprintf("%+v", imgs), fmt.Sprintf("%+v", link))
	pub := m.kit.blob.PublicBaseURL
	if pub == "" || !strings.HasSuffix(pub, "/_media/pub/") {
		t.Fatalf("PublicBaseURL %q — страж вхолостую", pub)
	}
	for i, b := range bodies {
		if strings.Contains(b, pub) || strings.Contains(b, "/_media/pub/") {
			t.Errorf("ответ %d несёт публичный адрес закрытого: %s", i, b)
		}
	}
	if len(bodies) < 9 {
		t.Fatalf("ответов %d — обход неполон", len(bodies))
	}
}
