// Package httpdev — dev-пути хранилища fs (спека media §6.3, R4 плана media): эмуляция S3 на публичном api вне
// контракта. POST /_blob/in — подписанная форма загрузки (как S3 POST: поля билета, файл последним полем «file»);
// GET /_media/pub/<ключ> — публичные варианты; GET /_media/priv/<ключ>?exp=…&sig=… — закрытые по подписи. Пути есть
// только при *_BLOB_BACKEND=fs: New отдаёт nil при s3 и none, и бинарник их не монтирует (TestDevPathsOnlyInFS); в
// прод-образе fs не стартует (config.LoadBlob, R15).
package httpdev

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/httpx"
)

// Пути dev-хранилища (их же подписывает blob.DevSigner).
const (
	UploadPath = "/_blob/in"
	PubPrefix  = "/_media/pub/"
	PrivPrefix = "/_media/priv/"
)

const (
	// formReserve — запас тела загрузки на поля формы и границы multipart сверх файла (R16).
	formReserve = 64 << 10
	// maxField — предел значения одного поля формы (политика — base64 JSON в сотни байт).
	maxField  = 8 << 10
	fileField = "file"

	cachePublic  = "public, max-age=86400"
	cachePrivate = "private, no-store"
)

// Handler — dev-пути одного процесса api.
type Handler struct {
	b       blob.Opened
	dev     *blob.DevSigner
	maxFile int64
	log     *slog.Logger
}

// New — dev-пути при fs (открытое хранилище с подписью DevSigner); при s3 и none — nil: путей нет. Предел файла —
// максимум MaxBytes целей реестра.
func New(o blob.Opened, reg *media.Registry, log *slog.Logger) *Handler {
	if !o.Available || o.Dev == nil {
		return nil
	}
	var maxFile int64
	for _, name := range reg.Names() {
		if p, ok := reg.Get(name); ok {
			maxFile = max(maxFile, p.MaxBytes)
		}
	}
	return &Handler{b: o, dev: o.Dev, maxFile: maxFile, log: log}
}

// UploadLimit — предел тела POST /_blob/in для httpx.LimitBody: максимум реестра и запас на поля формы (R16).
func (h *Handler) UploadLimit() int64 { return h.maxFile + formReserve }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == UploadPath:
		h.upload(w, r)
	case strings.HasPrefix(p, PubPrefix):
		h.serve(w, r, h.b.Buckets.Pub, strings.TrimPrefix(p, PubPrefix), cachePublic, false)
	case strings.HasPrefix(p, PrivPrefix):
		h.serve(w, r, h.b.Buckets.Priv, strings.TrimPrefix(p, PrivPrefix), cachePrivate, true)
	default:
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
	}
}

// errBadForm — форма не по правилам S3 POST (не multipart, файл не последним, дубль поля, нет файла): 400.
var errBadForm = errors.New("httpdev: форма загрузки")

// upload — подписанная форма (R4): поля читаются до файла, файл буферизуется (не больше предела реестра + 1), подпись,
// тип из поля формы (не из заголовка части), точный размер и срок проверяются до Put — отказ ничего не кладёт.
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	cors(w, r)
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed, "")
		return
	}
	fields, data, err := h.readForm(r)
	if err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, httpx.CodeRequestTooLarge, "")
			return
		}
		httpx.WriteProblem(w, r, http.StatusBadRequest, httpx.CodeRequestInvalid, "")
		return
	}
	key, err := h.dev.VerifyUpload(fields, int64(len(data)), fields[blob.FieldContentType])
	if err != nil {
		// подпись, ключ, тип, размер, срок — как AccessDenied у S3; причина клиенту не раскрывается
		httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeForbidden, "")
		return
	}
	if err := h.b.Buckets.In.Put(r.Context(), key, bytes.NewReader(data),
		blob.PutOptions{ContentType: fields[blob.FieldContentType]}); err != nil {
		httpx.WriteError(h.log, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readForm — поля формы и байты файла; файл — последним полем, после него частей нет.
func (h *Handler) readForm(r *http.Request) (map[string]string, []byte, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, errBadForm
	}
	fields := map[string]string{}
	var data []byte
	gotFile := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		name := part.FormName()
		switch {
		case gotFile, name == "":
			return nil, nil, errBadForm
		case name == fileField:
			// предел + 1: больший файл дойдёт до проверки размера и получит 403, как у S3 content-length-range
			if data, err = io.ReadAll(io.LimitReader(part, h.maxFile+1)); err != nil {
				return nil, nil, err
			}
			gotFile = true
			continue
		}
		if _, dup := fields[name]; dup {
			return nil, nil, errBadForm
		}
		v, err := io.ReadAll(io.LimitReader(part, maxField+1))
		if err != nil {
			return nil, nil, err
		}
		if len(v) > maxField {
			return nil, nil, errBadForm
		}
		fields[name] = string(v)
	}
	if !gotFile {
		return nil, nil, errBadForm
	}
	return fields, data, nil
}

// serve — вариант из бакета: только GET/HEAD; ключ — <id>/<вариант> (входящее <id> без «/» и обход каталога — 404);
// закрытое — только по подписи DevSigner (нет, чужая, истекла — 403). Заголовки ответа ставит обработчик: тип варианта,
// nosniff и Cache-Control бакета (у закрытого — private, no-store).
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, s blob.Store, key, cache string, signed bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed, "")
		return
	}
	if blob.ValidKey(key) != nil || !strings.Contains(key, "/") {
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
		return
	}
	if signed {
		q := r.URL.Query()
		if err := h.dev.VerifyGet(key, q.Get("exp"), q.Get("sig")); err != nil {
			httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeForbidden, "")
			return
		}
	}
	rc, info, err := s.Get(r.Context(), key)
	if errors.Is(err, blob.ErrNotFound) || errors.Is(err, blob.ErrBadKey) {
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
		return
	}
	if err != nil {
		httpx.WriteError(h.log, w, r, err)
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		httpx.WriteError(h.log, w, r, err)
		return
	}
	ct := info.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	hd := w.Header()
	hd.Set("Content-Type", ct)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", cache)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

// cors — CORS формы загрузки только для origin dev-фронтов (loopback по http/https): fs живёт лишь в dev и e2e, cookie у
// пути нет, подпись — единственная защита; чужому origin заголовков не ставим.
func cors(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !loopbackOrigin(origin) {
		return
	}
	h := w.Header()
	h.Add("Vary", "Origin")
	h.Set("Access-Control-Allow-Origin", origin)
	if r.Method == http.MethodOptions {
		h.Set("Access-Control-Allow-Methods", "POST")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Max-Age", "600")
	}
}

func loopbackOrigin(s string) bool {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
