//go:build wf_e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/media/mediatest"
)

// directForm — форма прямой загрузки: поля, затем файл (fileLast=false — поле после файла).
func directForm(t *testing.T, purpose string, ownerID uuid.UUID, data []byte, fileLast bool) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, kv := range [][2]string{{"purpose", purpose}, {"owner_id", ownerID.String()}, {"content_type", "image/png"}} {
		if err := w.WriteField(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", "asset.png")
	if err == nil {
		_, err = fw.Write(data)
	}
	if err == nil && !fileLast {
		err = w.WriteField("extra", "1")
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

// fieldsForm — форма прямой загрузки с полями fields по порядку (дубли и длина — как заданы), файл последним.
func fieldsForm(t *testing.T, fields [][2]string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, kv := range fields {
		if err := w.WriteField(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", "asset.png")
	if err == nil {
		_, err = fw.Write(data)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

// send — запрос к поднятому admin-api с cookie сотрудника; csrf — Origin и X-WF-Admin.
func (e *testEnv) send(method, path, tok string, body io.Reader, ct, idemKey string, csrf bool) (int, http.Header, string) {
	e.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+e.addr+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: admin.CookieName(true), Value: tok, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	}
	if csrf {
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("X-WF-Admin", "1")
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func adminPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := range 24 {
		for x := range 32 {
			img.Set(x, y, color.NRGBA{R: uint8(x * 8), G: 120, B: uint8(y * 10), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Спека media §3.6, §12a: тестовые ручки admin-api на собранном сервере (тег wf_e2e, хранилище fs) — прямая загрузка
// (multipart, файл последним, предел тела 5 МиБ — R16) создаёт объект uploaded с задачей media.process, повтор с тем же
// Idempotency-Key отдаёт тот же объект; снятие — taken_down с аудитом. Без сессии — 401, без CSRF — 403.
func TestMediaE2EAdmin(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	e.extra = []string{"ADMIN_BLOB_BACKEND=fs", "ADMIN_BLOB_FS_ROOT=" + t.TempDir(),
		"ADMIN_BLOB_FS_BASE_URL=http://127.0.0.1:8080", "ADMIN_BLOB_DEV_SIGN_KEY=" + e.key}
	e.startServer()
	_, tok := e.staffSession("media.admin@wf.local", "admin")
	data, owner := adminPNG(t), uuid.New()

	body, ct := directForm(t, mediatest.PurposeImage, owner, data, true)
	if code, _, b := e.send(http.MethodPost, "/_e2e/media/direct", "", body, ct, "", true); code != http.StatusUnauthorized {
		t.Fatalf("без сессии: %d %s", code, b)
	}
	body, ct = directForm(t, mediatest.PurposeImage, owner, data, true)
	if code, _, b := e.send(http.MethodPost, "/_e2e/media/direct", tok, body, ct, "", false); code != http.StatusForbidden ||
		!strings.Contains(b, "auth.csrf_failed") {
		t.Fatalf("без CSRF: %d %s", code, b)
	}
	big, bigCT := directForm(t, mediatest.PurposeImage, owner, make([]byte, 5<<20), true)
	if code, _, b := e.send(http.MethodPost, "/_e2e/media/direct", tok, big, bigCT, "", true); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("больше 5 МиБ: %d %s", code, b)
	}
	// ревью финала C m2: с Idempotency-Key — тоже 413 (идемпотентность читает тело до ручки)
	big, bigCT = directForm(t, mediatest.PurposeImage, owner, make([]byte, 5<<20), true)
	if code, _, b := e.send(http.MethodPost, "/_e2e/media/direct", tok, big, bigCT, uuid.NewString(), true); code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(b, "request.too_large") {
		t.Fatalf("больше 5 МиБ с ключом: %d %s", code, b)
	}
	body, ct = directForm(t, mediatest.PurposeImage, owner, data, false)
	if code, _, b := e.send(http.MethodPost, "/_e2e/media/direct", tok, body, ct, "", true); code != http.StatusBadRequest {
		t.Fatalf("файл не последним: %d %s", code, b)
	}
	// ревью Task 11 m3: поле длиннее 1 КиБ и дубль поля — 400, а не обрезка и перезапись
	for name, fields := range map[string][][2]string{
		"поле длиннее 1 КиБ": {{"purpose", mediatest.PurposeImage}, {"owner_id", owner.String()},
			{"content_type", "image/png" + strings.Repeat(" ", 1<<10)}},
		"дубль поля": {{"purpose", mediatest.PurposeImage}, {"owner_id", uuid.NewString()}, {"owner_id", owner.String()},
			{"content_type", "image/png"}},
	} {
		body, ct := fieldsForm(t, fields, data)
		if code, _, b := e.send(http.MethodPost, "/_e2e/media/direct", tok, body, ct, "", true); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, b)
		}
	}
	// ровно 1 КиБ — можно (граница)
	body, ct = fieldsForm(t, [][2]string{{"purpose", mediatest.PurposeImage}, {"owner_id", owner.String()},
		{"content_type", "image/png" + strings.Repeat(" ", 1<<10-len("image/png"))}}, data)
	if code, _, b := e.send(http.MethodPost, "/_e2e/media/direct", tok, body, ct, "", true); code != http.StatusCreated {
		t.Fatalf("поле ровно 1 КиБ: %d %s", code, b)
	}
	e.exec(`DELETE FROM river_job WHERE kind = 'media.process'`)
	e.exec(`DELETE FROM media_objects`)
	if n := e.count(`SELECT count(*) FROM media_objects`); n != 0 {
		t.Fatalf("отказы создали объектов: %d", n)
	}

	key := uuid.NewString()
	var ids []uuid.UUID
	// повтор после обрыва — те же байты тела (граница multipart та же): иначе это другой запрос (key_reused)
	form, ct := directForm(t, mediatest.PurposeImage, owner, data, true)
	for i := range 2 {
		code, hdr, b := e.send(http.MethodPost, "/_e2e/media/direct", tok, bytes.NewReader(form.Bytes()), ct, key, true)
		var img struct {
			ID    uuid.UUID `json:"id"`
			State string    `json:"state"`
		}
		if code != http.StatusCreated || json.Unmarshal([]byte(b), &img) != nil || img.State != "processing" {
			t.Fatalf("прямая загрузка #%d: %d %s", i+1, code, b)
		}
		if i == 1 && hdr.Get("Idempotency-Replayed") != "true" {
			t.Fatalf("повтор с тем же ключом не из сохранённого ответа: %v", hdr)
		}
		ids = append(ids, img.ID)
	}
	if ids[0] != ids[1] {
		t.Fatalf("повтор создал второй объект: %v", ids)
	}
	oid := ids[0]
	if n := e.count(`SELECT count(*) FROM media_objects WHERE id = $1 AND state = 'uploaded' AND uploaded_by_staff IS NOT NULL`, oid); n != 1 {
		t.Fatalf("объект прямой загрузки: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'media.process' AND args->>'object_id' = $1`, oid.String()); n != 1 {
		t.Fatalf("задач media.process: %d", n)
	}

	if code, _, b := e.send(http.MethodPost, "/_e2e/media/objects/"+oid.String()+"/take-down", tok, nil, "", uuid.NewString(), true); code != http.StatusNoContent {
		t.Fatalf("снятие: %d %s", code, b)
	}
	if n := e.count(`SELECT count(*) FROM media_objects WHERE id = $1 AND state = 'taken_down'`, oid); n != 1 {
		t.Fatal("объект не снят")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'media.object_taken_down' AND object_id = $1`, oid); n != 1 {
		t.Fatalf("аудит снятия: %d", n)
	}
}

// Пара TestAdminProdBuildHasNoE2ERoutes: в сборке wf_e2e тот же обход видит тестовые ручки — страж прод-сборки не
// вхолостую.
func TestAdminE2EBuildHasE2ERoutes(t *testing.T) {
	t.Parallel()
	if e2e, v1 := adminRoutes(t); e2e == 0 || v1 == 0 {
		t.Fatalf("маршрутов /_e2e: %d (ждали > 0), /v1/: %d", e2e, v1)
	}
}
