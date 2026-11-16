package domain_test

import (
	"errors"
	"strings"
	"testing"

	"wf/backend/internal/identity/internal/domain"
)

const stand = "terms=0.1.0|https://example.com/legal/terms,privacy=0.1.0|https://example.com/legal/privacy"

// Документы (спека §3.16): оба обязательны, ссылка https, версия — YYYY-MM-DD или semver, 1–32 символа.
func TestParseLegalDocuments(t *testing.T) {
	d, err := domain.ParseLegalDocuments(stand)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.LegalDoc{
		{Document: domain.DocTerms, Version: "0.1.0", URL: "https://example.com/legal/terms"},
		{Document: domain.DocPrivacy, Version: "0.1.0", URL: "https://example.com/legal/privacy"},
	}
	got := d.Items()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%+v", got)
	}
	// порядок в строке не важен, пробелы по краям частей — обрезаются, дата — годная версия
	d, err = domain.ParseLegalDocuments(" privacy=2026-11-01|https://wf.example/p , terms=1.2.3-rc.1+b5|https://wf.example/t ")
	if err != nil || d.Items()[0].Version != "1.2.3-rc.1+b5" || d.Items()[1].Version != "2026-11-01" {
		t.Fatalf("%v %+v", err, d.Items())
	}
	for _, bad := range []string{
		"",
		"terms=0.1.0|https://e.com/t", // один документ
		"terms=0.1.0|http://e.com/t,privacy=0.1.0|https://e.com/p",                                  // не https
		"terms=0.1.0|https:///t,privacy=0.1.0|https://e.com/p",                                      // нет хоста
		"terms=v0.1|https://e.com/t,privacy=0.1.0|https://e.com/p",                                  // не semver и не дата
		"terms=0.1|https://e.com/t,privacy=0.1.0|https://e.com/p",                                   // неполный semver
		"terms=01.1.0|https://e.com/t,privacy=0.1.0|https://e.com/p",                                // ведущий ноль semver
		"terms=2026-13-01|https://e.com/t,privacy=0.1.0|https://e.com/p",                            // нет такой даты
		"terms=|https://e.com/t,privacy=0.1.0|https://e.com/p",                                      // пустая версия
		"terms=0.1.0-" + strings.Repeat("a", 30) + "|https://e.com/t,privacy=0.1.0|https://e.com/p", // > 32
		"terms=0.1.0|https://e.com/t,terms=0.1.0|https://e.com/t",                                   // дубль
		"terms=0.1.0|https://e.com/t,privacy=0.1.0|https://e.com/p,rules=1.0.0|https://e.com/r",     // незнакомый
		"terms=0.1.0,privacy=0.1.0|https://e.com/p",                                                 // без ссылки
		"terms0.1.0|https://e.com/t,privacy=0.1.0|https://e.com/p",                                  // без =
		"мусор",
	} {
		if _, err := domain.ParseLegalDocuments(bad); !errors.Is(err, domain.ErrLegalConfig) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestLegalCheck(t *testing.T) {
	d, err := domain.ParseLegalDocuments(stand)
	if err != nil {
		t.Fatal(err)
	}
	ok := []domain.Accepted{{Document: "privacy", Version: "0.1.0"}, {Document: "terms", Version: "0.1.0"}}
	if err := d.Check(ok); err != nil {
		t.Fatal(err)
	}
	for name, acc := range map[string][]domain.Accepted{
		"пусто":                nil,
		"нет одного":           {{Document: "terms", Version: "0.1.0"}},
		"старая":               {{Document: "terms", Version: "0.0.9"}, {Document: "privacy", Version: "0.1.0"}},
		"лишний":               {{Document: "terms", Version: "0.1.0"}, {Document: "privacy", Version: "0.1.0"}, {Document: "rules", Version: "1"}},
		"дубль вместо второго": {{Document: "terms", Version: "0.1.0"}, {Document: "terms", Version: "0.1.0"}},
		"дубль":                {{Document: "terms", Version: "0.1.0"}, {Document: "privacy", Version: "0.1.0"}, {Document: "terms", Version: "0.1.0"}},
	} {
		if err := d.Check(acc); !errors.Is(err, domain.ErrDocumentsOutdated) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestIsDisposable(t *testing.T) {
	for d, want := range map[string]bool{
		"mailinator.com": true, "sub.mailinator.com": true, "a.b.mailinator.com": true, "MAILINATOR.COM.": true,
		"gmail.com": false, "yandex.ru": false, "mail.ru": false, "xmailinator.com": false, "com": false, "": false,
	} {
		if got := domain.IsDisposable(d); got != want {
			t.Errorf("%q: %v", d, got)
		}
	}
}

func TestNormalizeAppVersion(t *testing.T) {
	for in, want := range map[string]string{"1.4": "1.4.0", "1.4.0": "1.4.0", "0.0.1": "0.0.1", "10.20.30": "10.20.30"} {
		if got, ok := domain.NormalizeAppVersion(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "1", "1.4.0.1", "v1.4", "1.4-beta", "01.4", "1..4", "1.4.", "a.b.c", "1.1234567890.0", " 1.4"} {
		if _, ok := domain.NormalizeAppVersion(in); ok {
			t.Errorf("%q принят", in)
		}
	}
	if domain.MaxModelLen != 64 || domain.MaxOSVersionLen != 32 {
		t.Fatal("пределы строк устройства")
	}
}

// Десять реальных UA: браузер и ОС для «Где вы вошли» (R25).
func TestParseUserAgent(t *testing.T) {
	for _, c := range []struct{ ua, browser, os string }{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "Chrome", "Windows"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0", "Edge", "Windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:132.0) Gecko/20100101 Firefox/132.0", "Firefox", "macOS"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15", "Safari", "macOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1", "Safari", "iOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0.6723.90 Mobile/15E148 Safari/604.1", "Chrome", "iOS"},
		{"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36", "Samsung Internet", "Android"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36", "Chrome", "Android"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 OPR/115.0.0.0", "Opera", "Windows"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0", "Firefox", "Linux"},
		{"Mozilla/5.0 (iPad; CPU OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/132.0 Mobile/15E148 Safari/605.1.15", "Firefox", "iOS"},
		{"curl/8.10.1", "", ""},
		{"", "", ""},
	} {
		b, o := domain.ParseUserAgent(c.ua)
		if b != c.browser || o != c.os {
			t.Errorf("%.60s…: %q %q, ждали %q %q", c.ua, b, o, c.browser, c.os)
		}
	}
}
