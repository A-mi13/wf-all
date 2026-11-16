package ipgeo_test

import (
	"maps"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"wf/backend/internal/platform/ipgeo"
)

// fixture — вымышленная база в формате DB-IP «IP to City Lite»; генератор — backend/tools/ipgeofixture
// (go run ./ipgeofixture из backend/tools). Частные, петлевые, link-local и нулевые сети в ней нарочно
// с именами: их отсекает фильтр Lookup, а не отсутствие записи.
const fixture = "testdata/city-test.mmdb"

func open(t *testing.T) ipgeo.Locator {
	t.Helper()
	loc, closer, err := ipgeo.Open(fixture)
	if err != nil {
		t.Fatal(err)
	}
	// Windows держит открытый (mmap) файл — закрыть до уборки каталога теста.
	t.Cleanup(func() {
		if err := closer.Close(); err != nil {
			t.Error(err)
		}
	})
	return loc
}

// Спека identity §5.9: город по IP — имена city.names по языкам и код страны; вне базы, город без
// имён и адреса, которые не говорят о месте (частные, петля, link-local, нулевой), — false.
func TestLookupFixture(t *testing.T) {
	t.Parallel()
	loc := open(t)
	want := ipgeo.Place{Names: map[string]string{"ru": "Ставрополь", "en": "Stavropol"}, CountryCode: "RU"}
	for _, a := range []string{"203.0.113.7", "::ffff:203.0.113.7", "2001:db8:1::5"} {
		got, ok := loc.Lookup(netip.MustParseAddr(a))
		if !ok || got.CountryCode != want.CountryCode || !maps.Equal(got.Names, want.Names) {
			t.Errorf("Lookup(%s) = %+v %v, ждали %+v true", a, got, ok, want)
		}
	}
	for _, a := range []string{
		"8.8.8.8",       // вне фикстуры
		"198.51.100.1",  // есть страна, нет города
		"10.0.0.1",      // частный (в фикстуре — с именем)
		"192.168.1.1",   // частный
		"fd00::1",       // частный IPv6 (ULA)
		"127.0.0.1",     // петля
		"::1",           // петля IPv6
		"169.254.10.10", // link-local
		"fe80::1",       // link-local IPv6
		"0.0.0.0",       // нулевой
		"0.1.2.3",       // «эта сеть» 0.0.0.0/8 (RFC 1122), не только нулевой адрес
		"100.64.1.1",    // CGNAT 100.64.0.0/10 (RFC 6598): адрес провайдера, не места
		"::",            // нулевой IPv6
	} {
		if got, ok := loc.Lookup(netip.MustParseAddr(a)); ok {
			t.Errorf("Lookup(%s) = %+v, ждали false", a, got)
		}
	}
	if got, ok := loc.Lookup(netip.Addr{}); ok {
		t.Errorf("Lookup(нулевой netip.Addr) = %+v, ждали false", got)
	}
}

// Пустой путь (API_IPGEO_DB не задан) — города нет: None и Closer без действия.
func TestOpenEmptyPathIsNone(t *testing.T) {
	t.Parallel()
	loc, closer, err := ipgeo.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := loc.Lookup(netip.MustParseAddr("203.0.113.7")); ok {
		t.Fatal("пустой путь: Lookup нашёл город")
	}
	if _, ok := ipgeo.None.Lookup(netip.MustParseAddr("203.0.113.7")); ok {
		t.Fatal("None нашёл город")
	}
}

// Путь задан, файла нет — ошибка старта с путём (опечатку в API_IPGEO_DB видно сразу, а не «города нет»).
func TestOpenMissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "нет-файла.mmdb")
	_, _, err := ipgeo.Open(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("Open(%q): %v, ждали ошибку с путём", path, err)
	}
}

// Имя на языке аккаунта, иначе английское, иначе — нет.
func TestPlaceNameFallback(t *testing.T) {
	t.Parallel()
	p := ipgeo.Place{Names: map[string]string{"ru": "Ставрополь", "en": "Stavropol"}, CountryCode: "RU"}
	for loc, want := range map[string]string{"ru": "Ставрополь", "en": "Stavropol", "de": "Stavropol", "": "Stavropol"} {
		if got, ok := p.Name(loc); !ok || got != want {
			t.Errorf("Name(%q) = %q %v, ждали %q true", loc, got, ok, want)
		}
	}
	ruOnly := ipgeo.Place{Names: map[string]string{"ru": "Ставрополь"}}
	if got, ok := ruOnly.Name("de"); ok {
		t.Errorf("без en: Name(de) = %q, ждали false", got)
	}
	if got, ok := ruOnly.Name("ru"); !ok || got != "Ставрополь" {
		t.Errorf("без en: Name(ru) = %q %v", got, ok)
	}
	if got, ok := (ipgeo.Place{}).Name("ru"); ok {
		t.Errorf("пустое место: Name(ru) = %q, ждали false", got)
	}
}

// Файл не той базы (страна или ASN вместо города) в API_IPGEO_DB — ошибка старта с путём и типом, а не
// «города нет» у каждого входа.
func TestOpenRejectsNonCityDatabase(t *testing.T) {
	t.Parallel()
	const path = "testdata/country-test.mmdb"
	_, closer, err := ipgeo.Open(path)
	if err == nil {
		_ = closer.Close()
		t.Fatal("база стран принята как база городов")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "DBIP-Country-Lite") {
		t.Fatalf("ошибка без пути или типа базы: %v", err)
	}
}
