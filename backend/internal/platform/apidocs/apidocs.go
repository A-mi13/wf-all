// Package apidocs — Swagger UI по вшитому контракту: ручная проверка API на dev-стенде и
// справочник для клиентов (нативное приложение, веб). Включается флагом <ПРЕФИКС>_DOCS_ENABLED
// (config.HTTP); на проде выключен. Описания и примеры берутся из контракта — их полноту
// держит страж contracts/src/docs.mjs.
package apidocs

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Path — адрес страницы; контракт — Path+"/openapi.json".
const Path = "/docs"

// Swagger UI грузится с jsDelivr с закреплённой версией и SRI: файл, подменённый на CDN,
// браузер не исполнит. Обновление — новая версия и хэши
// (`curl -fsSL <url> | openssl dgst -sha384 -binary | openssl base64 -A`), docs/versions.md.
const (
	SwaggerUIVersion = "5.33.1"
	cssIntegrity     = "sha384-Ov4/wv3j2bmct8cDc5X4ngJZohVPzEmc6uDPH8WeljUxO5vtoykvMEfbu9Vh6RaW"
	jsIntegrity      = "sha384-ZPehFMQommnnuaZ4rpxgkgTT2DKFVp4hZC/7pLit+9Lek9T1YGSo23eHFbvNkXkw"
)

// Inline-скриптов и eval нет: запуск — init.js с нашего origin. style-src 'unsafe-inline' —
// Swagger UI ставит стили элементам сам. connect-src 'self' — «Try it out» ходит только сюда.
const csp = "default-src 'none'; script-src 'self' https://cdn.jsdelivr.net; " +
	"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data:; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

var (
	//go:embed index.html
	indexHTML string
	//go:embed init.js
	initJS []byte

	page = template.Must(template.New("index").Parse(indexHTML))
)

// Mount вешает на r страницу (Path), запуск Swagger UI и контракт specJSON. title — заголовок
// вкладки браузера.
func Mount(r chi.Router, title string, specJSON []byte) {
	var buf bytes.Buffer
	if err := page.Execute(&buf, map[string]string{
		"Title":        title,
		"Version":      SwaggerUIVersion,
		"CSSIntegrity": cssIntegrity,
		"JSIntegrity":  jsIntegrity,
	}); err != nil {
		panic(err) // шаблон вшит и проверен тестами: ошибка тут — ошибка сборки
	}
	html := buf.Bytes()

	r.Get(Path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		write(w, "text/html; charset=utf-8", html)
	})
	r.Get(Path+"/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, Path, http.StatusMovedPermanently)
	})
	r.Get(Path+"/init.js", func(w http.ResponseWriter, _ *http.Request) {
		write(w, "text/javascript; charset=utf-8", initJS)
	})
	r.Get(Path+"/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		write(w, "application/json", specJSON)
	})
}

func write(w http.ResponseWriter, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	// контракт меняется с каждым деплоем — браузер перепроверяет, а не берёт старый
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}
