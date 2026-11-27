package imaging

import (
	"bytes"
	"compress/zlib"
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// referenceP3 — эталонный профиль Display P3, который вписывается в варианты P3 (R8). Источник — Compact ICC
// Profiles (Clinton Ingram, github.com/saucecontrol/Compact-ICC-Profiles, profiles/DisplayP3-v4.icc, коммит
// ca0a34b4, 05.05.2023), лицензия CC0 1.0 — icc/LICENSE; 480 байт, SHA-256
// cb51de38e482ee974c0c76b9689e16aad04bad16e226fed2f30c842d15ff3a3d. Файл не правится: он совпадает с
// первоисточником; его desc — «sP3», поэтому распознаётся ещё и побайтовым равенством.
//
//go:embed icc/display-p3.icc
var referenceP3 []byte

// refPrimaries — rXYZ, gXYZ, bXYZ эталона (D50).
var refPrimaries = mustPrimaries(referenceP3)

const (
	maxICC       = 1 << 20 // предел распаковки iCCP: больше — zip-бомба, ErrCorrupt
	p3Tolerance  = 0.002   // допуск первичных к эталону (R8; профиль Apple отличается на 0.00002)
	headerLength = 128

	// Пределы разбора desc (mluc), ревью D-C1: каждая запись может указывать на весь тег, без пределов разбор
	// квадратичен (PNG в 421 байт — 7 ГиБ выделений). У профиля Apple «Display P3» и эталона — одна запись в десятки
	// байт; многоязычные профили — десятки локалей по десятку символов. Нарушение — описания нет, профиль не P3 (как
	// любой битый профиль: картинка не отклоняется, варианты без профиля).
	maxDescRecords     = 64   // записей
	maxDescRecordBytes = 512  // UTF-16 одной записи (256 символов)
	maxDescBytes       = 4096 // UTF-16 всех записей
)

// isDisplayP3 — R8 с поправкой спайка: профиль байт в байт равен эталону, или desc содержит «display p3» (без
// учёта регистра) и первичные в допуске ±0.002 от эталона. Битый профиль — не P3.
func isDisplayP3(p []byte) bool {
	if bytes.Equal(p, referenceP3) {
		return true
	}
	prim, ok := primaries(p)
	if !ok || !strings.Contains(strings.ToLower(iccDesc(p)), "display p3") {
		return false
	}
	for i := range prim {
		for j := range prim[i] {
			if d := prim[i][j] - refPrimaries[i][j]; d > p3Tolerance || d < -p3Tolerance {
				return false
			}
		}
	}
	return true
}

// tag — данные тега sig из таблицы тегов профиля; нет или выходит за профиль — nil.
func tag(p []byte, sig string) []byte {
	if len(p) < headerLength+4 {
		return nil
	}
	n := int(binary.BigEndian.Uint32(p[headerLength:]))
	if n > (len(p)-headerLength-4)/12 {
		return nil
	}
	for k := range n {
		e := p[headerLength+4+12*k:]
		if string(e[:4]) != sig {
			continue
		}
		off, size := int(binary.BigEndian.Uint32(e[4:])), int(binary.BigEndian.Uint32(e[8:]))
		if off+size > len(p) {
			return nil
		}
		return p[off : off+size]
	}
	return nil
}

// primaries — rXYZ, gXYZ, bXYZ (тип XYZ, s15Fixed16).
func primaries(p []byte) (out [3][3]float64, ok bool) {
	for i, sig := range []string{"rXYZ", "gXYZ", "bXYZ"} {
		t := tag(p, sig)
		if len(t) < 20 || string(t[:4]) != "XYZ " {
			return out, false
		}
		for j := range 3 {
			v := int64(binary.BigEndian.Uint32(t[8+4*j:])) // s15Fixed16: знаковое, дополнительный код
			if v >= 1<<31 {
				v -= 1 << 32
			}
			out[i][j] = float64(v) / 65536
		}
	}
	return out, true
}

// iccDesc — текст тега desc: mluc (ICC v4, UTF-16BE, все записи) или textDescription (v2, ASCII).
func iccDesc(p []byte) string {
	t := tag(p, "desc")
	switch {
	case len(t) >= 12 && string(t[:4]) == "desc":
		n := int(binary.BigEndian.Uint32(t[8:]))
		if n > len(t)-12 {
			return ""
		}
		return string(bytes.TrimRight(t[12:12+n], "\x00"))
	case len(t) >= 16 && string(t[:4]) == "mluc":
		recs, size := int(binary.BigEndian.Uint32(t[8:])), int(binary.BigEndian.Uint32(t[12:]))
		if size < 12 || recs > (len(t)-16)/size || recs > maxDescRecords {
			return ""
		}
		var sb strings.Builder
		total := 0
		for k := range recs {
			r := t[16+k*size:]
			l, off := int(binary.BigEndian.Uint32(r[4:])), int(binary.BigEndian.Uint32(r[8:]))
			total += l
			if l > maxDescRecordBytes || total > maxDescBytes || off+l > len(t) {
				return ""
			}
			s := t[off : off+l]
			u := make([]uint16, len(s)/2)
			for i := range u {
				u[i] = binary.BigEndian.Uint16(s[2*i:])
			}
			sb.WriteString(string(utf16.Decode(u)))
			sb.WriteByte('\n')
		}
		return sb.String()
	}
	return ""
}

func mustPrimaries(p []byte) [3][3]float64 {
	prim, ok := primaries(p)
	if !ok {
		panic("imaging: эталонный профиль P3 без первичных")
	}
	return prim
}

// inflateICCP — профиль из чанка iCCP: имя (1–79 байт), NUL, метод сжатия 0, zlib. Распаковка — не больше
// maxICC; больше или битые данные — ErrCorrupt.
func inflateICCP(d []byte) ([]byte, error) {
	z := bytes.IndexByte(d, 0)
	if z < 1 || z > 79 || z+2 > len(d) || d[z+1] != 0 {
		return nil, fmt.Errorf("%w: заголовок iCCP", ErrCorrupt)
	}
	zr, err := zlib.NewReader(bytes.NewReader(d[z+2:]))
	if err != nil {
		return nil, fmt.Errorf("%w: iCCP: %w", ErrCorrupt, err)
	}
	p, err := io.ReadAll(io.LimitReader(zr, maxICC+1))
	if err != nil {
		return nil, fmt.Errorf("%w: iCCP: %w", ErrCorrupt, err)
	}
	if len(p) > maxICC {
		return nil, fmt.Errorf("%w: iCCP больше %d байт", ErrCorrupt, maxICC)
	}
	return p, nil
}
