// Команда ipgeofixture — генератор тестовых баз города по IP (internal/platform/ipgeo/testdata):
// city-test.mmdb в формате DB-IP «IP to City Lite» (как GeoLite2-City: city.names, country.iso_code) и
// country-test.mmdb — база стран (DB-IP «IP to Country Lite»), которую ipgeo.Open обязан отвергнуть.
// Данные вымышленные: сети документации (RFC 5737, RFC 3849) и служебные диапазоны. Вывод воспроизводим
// байт в байт: BuildEpoch закреплён, порядок вставки фиксирован.
//
//	cd backend/tools && go run ./ipgeofixture
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

// buildEpoch — 2026-11-14T00:00:00Z: метаданные базы не зависят от часов машины.
const buildEpoch = 1794614400

type record struct {
	network string
	names   map[string]string // пусто — запись без города
	country string
}

// cityRecords — порядок вставки задаёт вывод; служебные сети нарочно с именем города: тест Lookup
// проверяет фильтр адресов, а не отсутствие записи.
var cityRecords = []record{
	{"203.0.113.0/24", map[string]string{"ru": "Ставрополь", "en": "Stavropol"}, "RU"},
	{"2001:db8:1::/48", map[string]string{"ru": "Ставрополь", "en": "Stavropol"}, "RU"},
	{"198.51.100.0/24", nil, "RU"},
	{"10.0.0.0/8", map[string]string{"ru": "Частноград", "en": "Privategrad"}, "RU"},
	{"192.168.0.0/16", map[string]string{"ru": "Частноград", "en": "Privategrad"}, "RU"},
	{"fd00::/8", map[string]string{"ru": "Частноград", "en": "Privategrad"}, "RU"},
	{"100.64.0.0/10", map[string]string{"ru": "Провайдерск", "en": "Carriergrad"}, "RU"},
	{"127.0.0.0/8", map[string]string{"ru": "Петлеград", "en": "Loopgrad"}, "RU"},
	{"::1/128", map[string]string{"ru": "Петлеград", "en": "Loopgrad"}, "RU"},
	{"169.254.0.0/16", map[string]string{"ru": "Линкоград", "en": "Linkgrad"}, "RU"},
	{"fe80::/10", map[string]string{"ru": "Линкоград", "en": "Linkgrad"}, "RU"},
	{"0.0.0.0/8", map[string]string{"ru": "Нульград", "en": "Zerograd"}, "RU"},
}

// countryRecords — база стран: записи без города.
var countryRecords = []record{
	{"203.0.113.0/24", nil, "RU"},
}

func main() {
	dir := flag.String("dir", "../internal/platform/ipgeo/testdata", "каталог для баз")
	flag.Parse()
	if err := write(filepath.Join(*dir, "city-test.mmdb"), "DBIP-City-Lite", cityRecords); err != nil {
		fmt.Fprintln(os.Stderr, "ipgeofixture:", err)
		os.Exit(1)
	}
	if err := write(filepath.Join(*dir, "country-test.mmdb"), "DBIP-Country-Lite", countryRecords); err != nil {
		fmt.Fprintln(os.Stderr, "ipgeofixture:", err)
		os.Exit(1)
	}
}

func write(out, dbType string, records []record) error {
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              buildEpoch,
		DatabaseType:            dbType,
		Description:             map[string]string{"en": "WhereFootball test fixture, fictional data"},
		IncludeReservedNetworks: true,
		Languages:               []string{"en", "ru"},
		RecordSize:              24,
	})
	if err != nil {
		return err
	}
	for _, r := range records {
		_, network, err := net.ParseCIDR(r.network)
		if err != nil {
			return err
		}
		rec := mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String(r.country)}}
		if len(r.names) > 0 {
			names := mmdbtype.Map{}
			for lang, name := range r.names {
				names[mmdbtype.String(lang)] = mmdbtype.String(name)
			}
			rec["city"] = mmdbtype.Map{"names": names}
		}
		if err := tree.Insert(network, rec); err != nil {
			return fmt.Errorf("%s: %w", r.network, err)
		}
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	if _, err := tree.WriteTo(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
