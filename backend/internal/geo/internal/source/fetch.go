package source

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"wf/backend/internal/geo/internal/domain"
)

// Ошибки загрузки: по тексту оператор и журнал импорта понимают, что не так с источником.
var (
	ErrInsecureURL     = errors.New("source: адрес источника — только https")
	ErrForeignRedirect = errors.New("source: редирект на другой хост")
	ErrTooLarge        = errors.New("source: файл больше лимита")
	ErrBadArchive      = errors.New("source: архив GeoNames не той формы")
	ErrBadCountry      = errors.New("source: код страны — две заглавные латинские буквы")
)

const (
	readmeEntry    = "readme.txt" // описание выгрузки в каждом архиве; не читается
	maxRedirects   = 5
	defaultTimeout = 15 * time.Minute
)

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

// Fetch скачивает и разбирает выгрузку страны: admin1CodesASCII.txt, <CC>.zip,
// alternatenames/<CC>.zip. Места отбираются при разборе (страна и domain.PlaceAllowed),
// названия — только отобранных мест и регионов, языки domain.SupportedLocales и без языка.
func Fetch(ctx context.Context, cfg FetchConfig, country string) (Raw, error) {
	if !countryRe.MatchString(country) {
		return Raw{}, fmt.Errorf("%w: %q", ErrBadCountry, country)
	}
	if cfg.MaxCompressed < 1 || cfg.MaxUncompressed < 1 {
		return Raw{}, errors.New("source: лимиты размера должны быть больше нуля")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return Raw{}, fmt.Errorf("%w: %q", ErrInsecureURL, cfg.BaseURL)
	}
	client := secureClient(cfg.HTTPClient, base.Host)
	dump := base.JoinPath("export", "dump")
	var raw Raw

	// admin1 — первым: его geonameid нужны отбору альтернативных названий
	body, file, err := download(ctx, client, dump.JoinPath("admin1CodesASCII.txt"), "admin1CodesASCII.txt", cfg.MaxCompressed)
	if err != nil {
		return Raw{Files: raw.Files}, err
	}
	raw.Files = append(raw.Files, file)
	if raw.Admin1, err = ParseAdmin1(bytes.NewReader(body), country); err != nil {
		return Raw{Files: raw.Files}, fmt.Errorf("source: admin1CodesASCII.txt: %w", err)
	}

	entry := country + ".txt"
	name := country + ".zip"
	rc, file, err := zipped(ctx, client, dump.JoinPath(name), name, entry, cfg)
	if file.Name != "" { // скачан — в журнал, даже если архив отвергнут (R27)
		raw.Files = append(raw.Files, file)
	}
	if err != nil {
		return Raw{Files: raw.Files}, err
	}
	raw.Places, err = parsePlaces(rc, func(p Place) bool {
		return p.CountryCode == country && domain.PlaceAllowed(p.FeatureClass, p.FeatureCode, p.Population)
	})
	_ = rc.Close()
	if err != nil {
		return Raw{Files: raw.Files}, fmt.Errorf("source: %s: %w", name, err)
	}

	ids := make(map[int64]bool, len(raw.Places)+len(raw.Admin1))
	for _, p := range raw.Places {
		ids[p.GeonameID] = true
	}
	for _, a := range raw.Admin1 {
		ids[a.GeonameID] = true
	}
	name = "alternatenames/" + country + ".zip"
	rc, file, err = zipped(ctx, client, dump.JoinPath("alternatenames", country+".zip"), name, entry, cfg)
	if file.Name != "" {
		raw.Files = append(raw.Files, file)
	}
	if err != nil {
		return Raw{Files: raw.Files}, err
	}
	raw.AltNames, err = parseAltNames(rc, func(a AltName) bool {
		return ids[a.GeonameID] && (a.Locale == "" || slices.Contains(domain.SupportedLocales, a.Locale))
	})
	_ = rc.Close()
	if err != nil {
		return Raw{Files: raw.Files}, fmt.Errorf("source: %s: %w", name, err)
	}
	return raw, nil
}

// secureClient — копия клиента (или клиент по умолчанию) с проверкой редиректов: только https
// и тот же host:port, что у BaseURL, не больше maxRedirects.
func secureClient(c *http.Client, host string) *http.Client {
	out := http.Client{Timeout: defaultTimeout}
	if c != nil {
		out = *c
	}
	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("source: больше %d редиректов", maxRedirects)
		}
		if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, host) {
			return fmt.Errorf("%w: %s://%s", ErrForeignRedirect, req.URL.Scheme, req.URL.Host)
		}
		return nil
	}
	return &out
}

// download читает файл в память не больше limit байт: Content-Length — до чтения, фактический
// размер — при чтении (ответ без длины).
func download(ctx context.Context, c *http.Client, u *url.URL, name string, limit int64) ([]byte, File, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, File{}, fmt.Errorf("source: %s: %w", name, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, File{}, fmt.Errorf("source: %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, File{}, fmt.Errorf("source: %s: HTTP %d", name, resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, File{}, fmt.Errorf("%w: %s — %d байт, лимит %d", ErrTooLarge, name, resp.ContentLength, limit)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, File{}, fmt.Errorf("source: %s: %w", name, err)
	}
	if int64(len(body)) > limit {
		return nil, File{}, fmt.Errorf("%w: %s — больше %d байт", ErrTooLarge, name, limit)
	}
	sum := sha256.Sum256(body)
	return body, File{Name: name, URL: resp.Request.URL.String(), Bytes: int64(len(body)),
		SHA256: hex.EncodeToString(sum[:])}, nil
}

// zipped скачивает архив и открывает в нём единственную запись entry. Архив скачан, но
// отвергнут (лишняя запись, не zip, больше лимита распаковки) — File возвращается вместе с
// ошибкой: отпечаток подменённого файла нужен журналу (R27).
func zipped(ctx context.Context, c *http.Client, u *url.URL, name, entry string, cfg FetchConfig) (io.ReadCloser, File, error) {
	body, file, err := download(ctx, c, u, name, cfg.MaxCompressed)
	if err != nil {
		return nil, File{}, err
	}
	rc, err := openEntry(body, entry, cfg.MaxUncompressed)
	if err != nil {
		return nil, file, fmt.Errorf("source: %s: %w", name, err)
	}
	return rc, file, nil
}

// openEntry — ровно одна запись want (и необязательная readme.txt); любая другая запись,
// повтор want, путь — ErrBadArchive. Размер — по заголовку и счётчиком при чтении.
func openEntry(body []byte, want string, limit int64) (io.ReadCloser, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadArchive, err)
	}
	var found *zip.File
	for _, f := range zr.File {
		switch {
		case f.Name == want && found == nil:
			found = f
		case f.Name == readmeEntry:
		default:
			return nil, fmt.Errorf("%w: лишняя запись %q", ErrBadArchive, f.Name)
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: нет записи %q", ErrBadArchive, want)
	}
	if found.UncompressedSize64 > uint64(limit) { //nolint:gosec // limit ≥ 1 проверен в Fetch
		return nil, fmt.Errorf("%w: %s — %d байт после распаковки, лимит %d", ErrTooLarge, want, found.UncompressedSize64, limit)
	}
	rc, err := found.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadArchive, err)
	}
	return &capReader{rc: rc, limit: limit}, nil
}

// capReader — ошибка ErrTooLarge, как только прочитано больше limit: заголовок zip может лгать.
type capReader struct {
	rc       io.ReadCloser
	n, limit int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	c.n += int64(n)
	if c.n > c.limit {
		return n, fmt.Errorf("%w: распакованная запись больше %d байт", ErrTooLarge, c.limit)
	}
	return n, err
}

func (c *capReader) Close() error { return c.rc.Close() }
