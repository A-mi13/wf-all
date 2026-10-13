package admin

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"wf/backend/internal/platform/httpx"
)

// Заголовки безопасности сборки админки (спека сотрудников §5.2). Сборка Vite — только свои
// скрипты и стили (style={…} React пишет через CSSOM — CSP его не режет); data: — картинки вроде QR.
const (
	staticCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; " +
		"frame-ancestors 'none'"
	cacheIndex  = "no-store"                            // новая сборка видна сразу
	cacheAssets = "public, max-age=31536000, immutable" // имена assets/ с хэшем содержимого
	cacheOther  = "no-cache"                            // favicon и прочее без хэша — со сверкой
	indexFile   = "index.html"
	assetsDir   = "assets/"
)

var errNoIndex = errors.New("admin: статика: index.html пропал из ADMIN_STATIC_DIR")

// static — собранная apps/admin из ADMIN_STATIC_DIR (решение D6: один origin без rewrite).
// Отвечает на всё, что не нашёл роутер: /v1/* — 404 Problem (API), остальное — файл каталога
// или index.html (маршрут SPA); нет файла в assets/ — 404, а не HTML под видом скрипта. Файл
// открывается os.OpenInRoot: путь вне каталога (.., ссылка наружу, абсолютный) не открывается;
// каталог не держится открытым — сборку можно заменить на месте.
type static struct {
	dir string // абсолютный
	log *slog.Logger
}

func newStatic(dir string, log *slog.Logger) (*static, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("admin: ADMIN_STATIC_DIR: %w", err)
	}
	s := &static{dir: abs, log: log}
	f, _, ok := s.open(indexFile)
	if !ok {
		return nil, fmt.Errorf("admin: ADMIN_STATIC_DIR %q: нет %s — каталог не сборка apps/admin", dir, indexFile)
	}
	_ = f.Close()
	return s, nil
}

func (s *static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Path; p == "/v1" || strings.HasPrefix(p, "/v1/") {
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed, "")
		return
	}
	name, ok := fileName(r.URL.Path)
	if !ok {
		httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
		return
	}
	f, st, ok := s.open(name)
	if !ok {
		if strings.HasPrefix(name, assetsDir) {
			httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
			return
		}
		name = indexFile
		if f, st, ok = s.open(name); !ok {
			httpx.WriteError(s.log, w, r, errNoIndex)
			return
		}
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Security-Policy", staticCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	switch {
	case name == indexFile:
		h.Set("Cache-Control", cacheIndex)
	case strings.HasPrefix(name, assetsDir):
		h.Set("Cache-Control", cacheAssets)
	default:
		h.Set("Cache-Control", cacheOther)
	}
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// open — обычный файл каталога; нет, каталог или путь наружу — false.
func (s *static) open(name string) (*os.File, fs.FileInfo, bool) {
	f, err := os.OpenInRoot(s.dir, name)
	if err != nil {
		return nil, nil, false
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, false
	}
	return f, st, true
}

// fileName — имя файла внутри каталога для пути запроса: корень — index.html; путь, негодный как
// имя внутри каталога (\, :, NUL — пути и диски Windows), — false.
func fileName(urlPath string) (string, bool) {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" {
		return indexFile, true
	}
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") {
		return "", false
	}
	return name, true
}
