// Package ipgeo — город по IP для «Где вы вошли» и писем о входе (спека identity §5.9): файл MMDB
// DB-IP «IP to City Lite» (CC BY 4.0, атрибуция — NOTICE), чтение — maxminddb-golang/v2. Путь — env
// API_IPGEO_DB; пусто — города нет (None). Сам IP игроку не отдаётся — только имя города.
package ipgeo

import (
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Place — город адреса: имена по языкам (ключи — коды локали из city.names MMDB) и ISO-код страны.
type Place struct {
	Names       map[string]string
	CountryCode string
}

// Name — имя на языке locale, иначе английское, иначе ok=false.
func (p Place) Name(locale string) (string, bool) {
	if n := p.Names[locale]; n != "" {
		return n, true
	}
	if n := p.Names["en"]; n != "" {
		return n, true
	}
	return "", false
}

// Locator — поиск города по адресу. ok=false — города нет (вне базы, служебный адрес, сбой чтения):
// город — подсказка для человека, его отсутствие не ошибка сценария.
type Locator interface {
	Lookup(ip netip.Addr) (Place, bool)
}

// None — города нет никогда (API_IPGEO_DB пуст).
var None Locator = none{}

type none struct{}

func (none) Lookup(netip.Addr) (Place, bool) { return Place{}, false }

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// Open — база по пути path. Пустой путь — None и Closer без действия; файла нет или он не MMDB —
// ошибка старта с путём. Closer освобождает файл (на Windows открытый mmap держит его).
func Open(path string) (Locator, io.Closer, error) {
	if path == "" {
		return None, nopCloser{}, nil
	}
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("ipgeo: база города по IP %q: %w", path, err)
	}
	// База стран или ASN тоже читается, но города в ней нет: без проверки опечатка в выборе файла
	// выглядела бы как «города нет» у каждого входа. Тип — DBIP-City-Lite, GeoLite2-City и т. п.
	if t := r.Metadata.DatabaseType; !strings.Contains(t, "City") {
		_ = r.Close()
		return nil, nil, fmt.Errorf("ipgeo: %q — база типа %q, нужна база городов (City)", path, t)
	}
	return &reader{r: r}, r, nil
}

type reader struct{ r *maxminddb.Reader }

// cityRecord — часть записи DB-IP City Lite (формат GeoLite2-City), которая нужна.
type cityRecord struct {
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

func (l *reader) Lookup(ip netip.Addr) (Place, bool) {
	ip = ip.Unmap()
	if !routable(ip) {
		return Place{}, false
	}
	res := l.r.Lookup(ip)
	if !res.Found() {
		return Place{}, false
	}
	var rec cityRecord
	if err := res.Decode(&rec); err != nil || len(rec.City.Names) == 0 {
		return Place{}, false
	}
	return Place{Names: rec.City.Names, CountryCode: rec.Country.ISOCode}, true
}

// Сети, которых нет среди проверок netip: «эта сеть» 0.0.0.0/8 (RFC 1122, шире нулевого адреса) и
// CGNAT 100.64.0.0/10 (RFC 6598: общий адрес провайдера, место по нему не определить).
var (
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	cgnat       = netip.MustParsePrefix("100.64.0.0/10")
)

// routable — адрес, который может говорить о месте: не пустой, не из 0.0.0.0/8 и не нулевой IPv6, не
// частный, не CGNAT, не петля, не link-local и не multicast. Служебные адреса базой не читаются: их
// город был бы случайным (запрос через прокси без peer, локальная сеть, сеть оператора).
func routable(ip netip.Addr) bool {
	return ip.IsValid() && !ip.IsUnspecified() && !thisNetwork.Contains(ip) && !ip.IsPrivate() &&
		!cgnat.Contains(ip) && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}
