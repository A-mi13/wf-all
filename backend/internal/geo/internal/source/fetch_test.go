package source_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"wf/backend/internal/geo/internal/source"
)

type entry struct{ name, body string }

func zipOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const readme = "GeoNames: описание выгрузки (в тесте — заглушка)\n"

const (
	pathAdmin1 = "/export/dump/admin1CodesASCII.txt"
	pathPlaces = "/export/dump/RU.zip"
	pathAlt    = "/export/dump/alternatenames/RU.zip"
)

// dump — выгрузка страны RU по путям download.geonames.org; архивы — как настоящие (readme + RU.txt).
func dump(t *testing.T) map[string][]byte {
	t.Helper()
	return map[string][]byte{
		pathAdmin1: readTestdata(t, "admin1CodesASCII.txt"),
		pathPlaces: zipOf(t, entry{"readme.txt", readme}, entry{"RU.txt", string(readTestdata(t, "RU.txt"))}),
		pathAlt:    zipOf(t, entry{"readme.txt", readme}, entry{"RU.txt", string(readTestdata(t, "alternateNamesV2-RU.txt"))}),
	}
}

// site — TLS-сервер выгрузки. redirects: путь → адрес (абсолютный или путь этого сервера);
// chunked — ответ без Content-Length (размер узнаётся только чтением).
type site struct {
	files     map[string][]byte
	redirects map[string]string
	chunked   bool
}

func (s site) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to, ok := s.redirects[r.URL.Path]; ok {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		b, ok := s.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if s.chunked {
			w.(http.Flusher).Flush()
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fetchConfig — клиент тестового сервера (его сертификат системе не известен); все серверы
// httptest используют один сертификат, поэтому клиент доверяет и «чужому» серверу.
func fetchConfig(srv *httptest.Server) source.FetchConfig {
	return source.FetchConfig{BaseURL: srv.URL, MaxCompressed: 1 << 20, MaxUncompressed: 1 << 20, HTTPClient: srv.Client()}
}

func TestFetch(t *testing.T) {
	files := dump(t)
	srv := site{files: files}.serve(t)
	raw, err := source.Fetch(context.Background(), fetchConfig(srv), "RU")
	if err != nil {
		t.Fatal(err)
	}
	// отбор §5.4 при разборе: без PPLX (Зюзино), PPLQ (Орловка), деревни без населения (802557)
	var ids []int64
	for _, p := range raw.Places {
		ids = append(ids, p.GeonameID)
	}
	if !slices.Equal(ids, []int64{487846, 493702, 526815}) {
		t.Fatalf("места: %v", ids)
	}
	if len(raw.Admin1) != 3 {
		t.Fatalf("admin1: %+v", raw.Admin1)
	}
	// названия — только отобранных мест и регионов admin1, языки ru, en и без языка
	keep := map[int64]bool{487846: true, 493702: true, 526815: true, 487839: true, 1490542: true, 524894: true}
	for _, a := range raw.AltNames {
		if !keep[a.GeonameID] || (a.Locale != "" && a.Locale != "ru" && a.Locale != "en") {
			t.Errorf("лишнее название: %+v", a)
		}
	}
	if len(raw.AltNames) != 33 {
		t.Fatalf("названий %d, ждали 33", len(raw.AltNames))
	}
	// журнал: имя, итоговый адрес, размер и sha256 скачанного файла
	names := []string{"admin1CodesASCII.txt", "RU.zip", "alternatenames/RU.zip"}
	paths := []string{pathAdmin1, pathPlaces, pathAlt}
	if len(raw.Files) != 3 {
		t.Fatalf("файлы: %+v", raw.Files)
	}
	for i, f := range raw.Files {
		sum := sha256.Sum256(files[paths[i]])
		if f.Name != names[i] || f.URL != srv.URL+paths[i] || f.Bytes != int64(len(files[paths[i]])) ||
			f.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("файл %d: %+v", i, f)
		}
	}
}

func TestFetchFollowsSameHostRedirect(t *testing.T) {
	files := dump(t)
	files["/mirror/RU.zip"] = files[pathPlaces]
	srv := site{files: files, redirects: map[string]string{pathPlaces: "/mirror/RU.zip"}}.serve(t)
	raw, err := source.Fetch(context.Background(), fetchConfig(srv), "RU")
	if err != nil {
		t.Fatal(err)
	}
	if raw.Files[1].URL != srv.URL+"/mirror/RU.zip" {
		t.Fatalf("итоговый адрес: %s", raw.Files[1].URL)
	}
}

// Review Focus 3: редирект на чужой хост. Чужой сервер отдаёт настоящий файл — без проверки
// переход прошёл бы успешно.
func TestFetchRejectsForeignRedirect(t *testing.T) {
	files := dump(t)
	foreign := site{files: files}.serve(t)
	srv := site{files: files, redirects: map[string]string{pathPlaces: foreign.URL + pathPlaces}}.serve(t)
	if _, err := source.Fetch(context.Background(), fetchConfig(srv), "RU"); !errors.Is(err, source.ErrForeignRedirect) {
		t.Fatalf("другой хост: %v", err)
	}
	// тот же хост, но http — тоже отказ (схема проверяется отдельно от хоста)
	plain := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+r.URL.Path, http.StatusFound)
	}))
	defer plain.Close()
	if _, err := source.Fetch(context.Background(), fetchConfig(plain), "RU"); !errors.Is(err, source.ErrForeignRedirect) {
		t.Fatalf("тот же хост, http: %v", err)
	}
}

func TestFetchRequiresHTTPS(t *testing.T) {
	for _, base := range []string{"http://download.geonames.org", "", "download.geonames.org", "ftp://download.geonames.org"} {
		cfg := source.FetchConfig{BaseURL: base, MaxCompressed: 1, MaxUncompressed: 1}
		if _, err := source.Fetch(context.Background(), cfg, "RU"); !errors.Is(err, source.ErrInsecureURL) {
			t.Errorf("%q: %v", base, err)
		}
	}
}

// Код страны попадает в путь запроса: проверяется до первого запроса.
func TestFetchRejectsBadCountry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("запрос до проверки кода страны")
	}))
	defer srv.Close()
	for _, cc := range []string{"ru", "RUS", "R1", "../", ""} {
		if _, err := source.Fetch(context.Background(), fetchConfig(srv), cc); !errors.Is(err, source.ErrBadCountry) {
			t.Errorf("%q: %v", cc, err)
		}
	}
}

// Review Focus 3: подменённый архив — из zip читается ровно одна ожидаемая запись.
func TestFetchRejectsUnexpectedArchive(t *testing.T) {
	ru := string(readTestdata(t, "RU.txt"))
	cases := map[string][]byte{
		"лишняя запись":     zipOf(t, entry{"readme.txt", readme}, entry{"RU.txt", ru}, entry{"evil.sh", "echo pwned"}),
		"запись дважды":     zipOf(t, entry{"RU.txt", ru}, entry{"RU.txt", ru}),
		"путь в записи":     zipOf(t, entry{"../RU.txt", ru}),
		"нет записи страны": zipOf(t, entry{"readme.txt", readme}),
		"не zip":            []byte("это не zip"),
		// оборванное скачивание: центрального каталога в конце архива нет
		"обрезан при скачивании": func() []byte { b := zipOf(t, entry{"readme.txt", readme}, entry{"RU.txt", ru}); return b[:len(b)/2] }(),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			files := dump(t)
			files[pathPlaces] = body
			srv := site{files: files}.serve(t)
			if _, err := source.Fetch(context.Background(), fetchConfig(srv), "RU"); !errors.Is(err, source.ErrBadArchive) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// Review Focus 3: файл больше лимита — и по Content-Length, и по факту чтения (ответ без длины),
// и после распаковки. Ровно в лимите — проходит: лимит на каждый файл, а не на сумму.
func TestFetchLimits(t *testing.T) {
	files := dump(t)
	ctx := context.Background()
	for _, chunked := range []bool{false, true} {
		srv := site{files: files, chunked: chunked}.serve(t)
		cfg := fetchConfig(srv)
		cfg.MaxCompressed = int64(len(files[pathPlaces])) - 1
		if _, err := source.Fetch(ctx, cfg, "RU"); !errors.Is(err, source.ErrTooLarge) {
			t.Errorf("сжатый, chunked=%v: %v", chunked, err)
		}
	}
	srv := site{files: files}.serve(t)
	cfg := fetchConfig(srv)
	cfg.MaxUncompressed = int64(len(readTestdata(t, "RU.txt"))) - 1
	if _, err := source.Fetch(ctx, cfg, "RU"); !errors.Is(err, source.ErrTooLarge) {
		t.Errorf("распакованный: %v", err)
	}
	cfg = fetchConfig(srv)
	cfg.MaxCompressed = int64(max(len(files[pathAdmin1]), len(files[pathPlaces]), len(files[pathAlt])))
	cfg.MaxUncompressed = int64(max(len(readTestdata(t, "RU.txt")), len(readTestdata(t, "alternateNamesV2-RU.txt"))))
	if _, err := source.Fetch(ctx, cfg, "RU"); err != nil {
		t.Errorf("ровно в лимите: %v", err)
	}
}

func TestFetchHTTPError(t *testing.T) {
	files := dump(t)
	delete(files, pathAdmin1)
	srv := site{files: files}.serve(t)
	if _, err := source.Fetch(context.Background(), fetchConfig(srv), "RU"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
}

// R27: второй файл отвергнут (битая строка или подменённый архив) — уже скачанные файлы с
// отпечатками, включая отвергнутый, возвращаются вместе с ошибкой (журнал импорта хранит sha256
// именно подменённого файла).
func TestFetchKeepsDownloadedFilesOnError(t *testing.T) {
	for name, tc := range map[string]struct {
		body []byte
		is   error
	}{
		"битая строка":  {zipOf(t, entry{"RU.txt", "1\tобрыв\n"}), source.ErrFormat},
		"лишняя запись": {zipOf(t, entry{"RU.txt", "x"}, entry{"evil.sh", "echo pwned"}), source.ErrBadArchive},
	} {
		t.Run(name, func(t *testing.T) {
			files := dump(t)
			files[pathPlaces] = tc.body
			srv := site{files: files}.serve(t)
			raw, err := source.Fetch(context.Background(), fetchConfig(srv), "RU")
			if !errors.Is(err, tc.is) {
				t.Fatalf("err = %v", err)
			}
			sum := sha256.Sum256(tc.body)
			if len(raw.Files) != 2 || raw.Files[1].Name != "RU.zip" || raw.Files[1].SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("файлы при ошибке: %+v", raw.Files)
			}
			if raw.Places != nil || raw.AltNames != nil || raw.Admin1 != nil {
				t.Fatalf("при ошибке — только файлы: %+v", raw)
			}
		})
	}
}
