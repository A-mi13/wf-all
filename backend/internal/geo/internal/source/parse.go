package source

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Колонки файлов GeoNames (readme.txt выгрузки; формат проверен 02.10.2026).
const (
	placeColumns  = 19 // geonameid … modification date
	altColumns    = 10 // alternateNameId … isHistoric, from, to
	admin1Columns = 4  // code, name, name ascii, geonameid
	// колонка alternatenames основного файла — до 10 000 символов (в UTF-8 до 40 КБ с запасом)
	maxLineBytes = 1 << 20
)

// ErrFormat — строка не в формате GeoNames: файл обрезан, подменён или формат сменился.
var ErrFormat = errors.New("source: строка не в формате GeoNames")

// ParsePlaces — основной файл страны целиком, без отбора.
func ParsePlaces(r io.Reader) ([]Place, error) { return parsePlaces(r, nil) }

// ParseAltNames — alternateNamesV2 целиком, без отбора.
func ParseAltNames(r io.Reader) ([]AltName, error) { return parseAltNames(r, nil) }

// ParseAdmin1 — строки admin1CodesASCII.txt страны country (файл общий на весь мир).
func ParseAdmin1(r io.Reader, country string) ([]Admin1, error) {
	var out []Admin1
	err := eachRow(r, admin1Columns, func(f []string) error {
		cc, code, ok := strings.Cut(f[0], ".")
		if !ok || cc == "" || code == "" {
			return fmt.Errorf("код %q — нужен <страна>.<код>", f[0])
		}
		gid, err := parseID(f[3])
		if err != nil {
			return err
		}
		if cc == country {
			out = append(out, Admin1{CountryCode: cc, Code: code, Name: f[1], ASCIIName: f[2], GeonameID: gid})
		}
		return nil
	})
	return out, err
}

// parsePlaces — разбор с отбором keep (nil — всё): Fetch не держит в памяти лишнее.
func parsePlaces(r io.Reader, keep func(Place) bool) ([]Place, error) {
	var out []Place
	err := eachRow(r, placeColumns, func(f []string) error {
		id, err := parseID(f[0])
		if err != nil {
			return err
		}
		lat, err := parseCoord(f[4], 90)
		if err != nil {
			return err
		}
		lon, err := parseCoord(f[5], 180)
		if err != nil {
			return err
		}
		var pop int64
		if f[14] != "" {
			if pop, err = strconv.ParseInt(f[14], 10, 64); err != nil || pop < 0 {
				return fmt.Errorf("население %q", f[14])
			}
		}
		p := Place{GeonameID: id, Name: f[1], ASCIIName: f[2], Lat: lat, Lon: lon, FeatureClass: f[6],
			FeatureCode: f[7], CountryCode: f[8], Admin1Code: f[10], Population: pop, Timezone: f[17]}
		if keep == nil || keep(p) {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

func parseAltNames(r io.Reader, keep func(AltName) bool) ([]AltName, error) {
	var out []AltName
	err := eachRow(r, altColumns, func(f []string) error {
		id, err := parseID(f[0])
		if err != nil {
			return err
		}
		gid, err := parseID(f[1])
		if err != nil {
			return err
		}
		var flags [4]bool // isPreferredName, isShortName, isColloquial, isHistoric
		for i := range flags {
			if flags[i], err = parseFlag(f[4+i]); err != nil {
				return err
			}
		}
		a := AltName{ID: id, GeonameID: gid, Locale: f[2], Name: f[3], Preferred: flags[0], Short: flags[1],
			Colloquial: flags[2], Historic: flags[3], To: f[9]}
		if keep == nil || keep(a) {
			out = append(out, a)
		}
		return nil
	})
	return out, err
}

// eachRow — строки TSV ровно из columns колонок; ошибка — с номером строки и ErrFormat.
// Пустые строки пропускаются, \r в конце срезается.
func eachRow(r io.Reader, columns int, fn func(f []string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != columns {
			return fmt.Errorf("%w: строка %d: %d колонок, ждали %d", ErrFormat, n, len(f), columns)
		}
		if err := fn(f); err != nil {
			return fmt.Errorf("%w: строка %d: %w", ErrFormat, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("source: чтение: %w", err)
	}
	return nil
}

func parseID(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 1 {
		return 0, fmt.Errorf("id %q", s)
	}
	return v, nil
}

// parseCoord — число в [-limit, limit]; NaN и ±Inf — ошибка (NaN не ловится сравнениями).
func parseCoord(s string, limit float64) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || v < -limit || v > limit {
		return 0, fmt.Errorf("координата %q", s)
	}
	return v, nil
}

func parseFlag(s string) (bool, error) {
	switch s {
	case "":
		return false, nil
	case "1":
		return true, nil
	}
	return false, fmt.Errorf("флаг %q — ждали пусто или 1", s)
}
