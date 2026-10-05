package source_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"wf/backend/internal/geo/internal/source"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // выдержки GeoNames этого пакета
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// line — строка n (с нуля) выдержки, разбитая на колонки.
func line(t *testing.T, name string, n int) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(string(readTestdata(t, name)), "\n"), "\n")
	return strings.Split(lines[n], "\t")
}

func TestParsePlaces(t *testing.T) {
	places, err := source.ParsePlaces(bytes.NewReader(readTestdata(t, "RU.txt")))
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 6 {
		t.Fatalf("мест %d, ждали 6 — Parse* не фильтруют", len(places))
	}
	want := source.Place{GeonameID: 487846, Name: "Stavropol", ASCIIName: "Stavropol", FeatureClass: "P",
		FeatureCode: "PPLA", CountryCode: "RU", Admin1Code: "70", Timezone: "Europe/Moscow",
		Population: 433931, Lat: 45.03442, Lon: 41.9642}
	if places[1] != want {
		t.Fatalf("Ставрополь:\n got %+v\nwant %+v", places[1], want)
	}
	if p := places[0]; p.GeonameID != 461740 || p.FeatureCode != "PPLX" || p.Population != 121000 {
		t.Fatalf("Зюзино: %+v", p)
	}
	if p := places[5]; p.GeonameID != 12628120 || p.Timezone != "" || p.Population != 0 || p.Lon != 137.22764 {
		t.Fatalf("Орловка (пустая таймзона): %+v", p)
	}
}

// Колонка alternatenames — до 10 000 символов; стандартный буфер Scanner (64 КБ) на длинной строке
// дал бы bufio.ErrTooLong.
func TestParsePlacesLongLine(t *testing.T) {
	f := line(t, "RU.txt", 1)
	f[3] = strings.Repeat("Stavropol,", 20000) // 200 КБ
	places, err := source.ParsePlaces(strings.NewReader(strings.Join(f, "\t") + "\n"))
	if err != nil || len(places) != 1 || places[0].GeonameID != 487846 {
		t.Fatalf("%v %+v", err, places)
	}
}

func TestParsePlacesRejectsBrokenRows(t *testing.T) {
	good := line(t, "RU.txt", 1)
	with := func(i int, v string) string {
		f := append([]string(nil), good...)
		f[i] = v
		return strings.Join(f, "\t")
	}
	cases := map[string]string{
		"18 колонок":              strings.Join(good[:18], "\t"),
		"20 колонок":              strings.Join(good, "\t") + "\tлишнее",
		"id не число":             with(0, "abc"),
		"id ноль":                 with(0, "0"),
		"широта NaN":              with(4, "NaN"),
		"широта за 90":            with(4, "90.5"),
		"долгота Inf":             with(5, "+Inf"),
		"долгота за -180":         with(5, "-180.01"),
		"население отрицательное": with(14, "-5"),
		"население не число":      with(14, "много"),
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := source.ParsePlaces(strings.NewReader(row + "\n"))
			if !errors.Is(err, source.ErrFormat) || !strings.Contains(err.Error(), "строка 1") {
				t.Fatalf("err = %v — ждали ErrFormat с номером строки", err)
			}
		})
	}
	// обрезанный файл: последняя строка оборвана посреди колонок
	data := readTestdata(t, "RU.txt")
	_, err := source.ParsePlaces(bytes.NewReader(data[:len(data)-40]))
	if !errors.Is(err, source.ErrFormat) || !strings.Contains(err.Error(), "строка 6") {
		t.Fatalf("обрезанный файл: %v", err)
	}
}

func TestParseAltNames(t *testing.T) {
	alts, err := source.ParseAltNames(bytes.NewReader(readTestdata(t, "alternateNamesV2-RU.txt")))
	if err != nil {
		t.Fatal(err)
	}
	if len(alts) != 46 {
		t.Fatalf("названий %d, ждали 46", len(alts))
	}
	byID := map[int64]source.AltName{}
	for _, a := range alts {
		byID[a.ID] = a
	}
	want := map[int64]source.AltName{
		// предпочтительное и краткое ru «Ставрополье» (спека §3.4)
		2298599: {ID: 2298599, GeonameID: 487839, Locale: "ru", Name: "Ставрополье", Preferred: true, Short: true},
		5961934: {ID: 5961934, GeonameID: 487846, Locale: "ru", Name: "Ставрополь"},
		2426310: {ID: 2426310, GeonameID: 487846, Locale: "", Name: "Ставрополь", Preferred: true},
		11389239: {ID: 11389239, GeonameID: 463825, Locale: "ru", Name: "Зеленоградский Район",
			Historic: true, To: "2015"},
		1738212: {ID: 1738212, GeonameID: 464312, Locale: "ru", Name: "Зашеек", Preferred: true, To: "2018"},
		5702223: {ID: 5702223, GeonameID: 487839, Locale: "link",
			Name: "https://en.wikipedia.org/wiki/Stavropol_Krai"},
	}
	for id, w := range want {
		if byID[id] != w {
			t.Errorf("%d:\n got %+v\nwant %+v", id, byID[id], w)
		}
	}
}

func TestParseAltNamesRejectsBrokenRows(t *testing.T) {
	good := line(t, "alternateNamesV2-RU.txt", 0) // 7948122 Зюзино, предпочтительное
	flag := append([]string(nil), good...)
	flag[4] = "2"
	for name, row := range map[string]string{
		"9 колонок":          strings.Join(good[:9], "\t"),
		"флаг не 1":          strings.Join(flag, "\t"),
		"geonameid не число": strings.Replace(strings.Join(good, "\t"), "461740", "x", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := source.ParseAltNames(strings.NewReader(row + "\n")); !errors.Is(err, source.ErrFormat) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestParseAdmin1(t *testing.T) {
	data := string(readTestdata(t, "admin1CodesASCII.txt")) + "ZZ.01\tSynthetic\tSynthetic\t1\n"
	a, err := source.ParseAdmin1(strings.NewReader(data), "RU")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 3 {
		t.Fatalf("регионов %d, ждали 3 — чужая страна ZZ должна отсеяться: %+v", len(a), a)
	}
	want := source.Admin1{CountryCode: "RU", Code: "70", Name: "Stavropol Kray", ASCIIName: "Stavropol Kray", GeonameID: 487839}
	if a[1] != want {
		t.Fatalf("got %+v want %+v", a[1], want)
	}
	for name, row := range map[string]string{
		"код без точки": "RU70\tX\tX\t1",
		"3 колонки":     "RU.70\tX\tX",
		"geonameid":     "RU.70\tX\tX\tx",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := source.ParseAdmin1(strings.NewReader(row+"\n"), "RU"); !errors.Is(err, source.ErrFormat) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// Ошибка чтения посреди строки: Scanner отдаёт оборванный остаток как последнюю строку; он не
// должен сойти за ErrFormat «N колонок» — причина в ошибке чтения.
func TestParseReadErrorBeforePartialLine(t *testing.T) {
	boom := errors.New("обрыв соединения")
	partial := func() io.Reader { return io.MultiReader(strings.NewReader("1\tобрыв"), iotest.ErrReader(boom)) }
	for name, parse := range map[string]func() error{
		"places":   func() error { _, err := source.ParsePlaces(partial()); return err },
		"altnames": func() error { _, err := source.ParseAltNames(partial()); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := parse()
			if !errors.Is(err, boom) || errors.Is(err, source.ErrFormat) {
				t.Fatalf("err = %v — ждали ошибку чтения, не ErrFormat", err)
			}
			if n := strings.Count(err.Error(), "source:"); n != 1 {
				t.Fatalf("«source:» %d раз(а) в %q", n, err)
			}
		})
	}
}
