package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	xwebp "golang.org/x/image/webp"
)

// info — всё, что читается до декодирования пикселей.
type info struct {
	Header
	rawW, rawH  int    // размеры в файле, до ориентации (по ним выделяет память декодер)
	orientation int    // EXIF 0x0112, 1–8; нет или мусор — 1
	icc         []byte // профиль JPEG (склеенный APP2) или WebP (ICCP); только для распознавания P3
	iccp        []byte // данные чанка iCCP PNG (сжаты, распаковываются с пределом в Process)
	jpeg        jpegMarkers
}

// jpegMarkers — то, по чему image/jpeg выделяет память и тратит время: первый кадр SOF0/1/2, число сканов, признаки
// цвета (JFIF, APP14 Adobe — по всему файлу: декодер читает их и между сканами).
type jpegMarkers struct {
	progressive    bool
	comps          []jpegComp // компоненты кадра в порядке SOF; нет кадра — пусто
	scans          int
	jfif           bool // последний APP0 — «JFIF\0» (как d.jfif)
	adobe          bool // был APP14 «Adobe»
	adobeTransform byte // transform последнего APP14 «Adobe»
}

type jpegComp struct{ id, h, v int }

// inspect — сигнатура, размеры (DecodeConfig того же декодера, что потом выделит память), анимация и метаданные.
// Разбор метаданных снисходителен: битый сегмент лишь прекращает разбор, а «битый ли файл» решает декодер.
func inspect(b []byte) (info, error) {
	in := info{Header: Header{Kind: kindOf(b)}, orientation: 1}
	var (
		cfg image.Config
		err error
	)
	r := bytes.NewReader(b)
	switch in.Kind {
	case KindJPEG:
		var exif []byte
		exif, in.icc, in.jpeg = walkJPEG(b)
		in.orientation = exifOrientation(exif)
		cfg, err = jpeg.DecodeConfig(r)
	case KindPNG:
		in.Animated, in.iccp = walkPNG(b)
		cfg, err = png.DecodeConfig(r)
	case KindWebP:
		in.Animated, in.icc = walkWebP(b)
		cfg, err = xwebp.DecodeConfig(r)
	default:
		return info{}, ErrUnsupported
	}
	if err != nil {
		return info{}, decodeError(err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return info{}, fmt.Errorf("%w: размеры %d×%d", ErrCorrupt, cfg.Width, cfg.Height)
	}
	in.rawW, in.rawH = cfg.Width, cfg.Height
	in.Width, in.Height = cfg.Width, cfg.Height
	if in.orientation >= 5 {
		in.Width, in.Height = cfg.Height, cfg.Width
	}
	return in, nil
}

// decodeError — ошибка декодера → отказ: неподдерживаемый вариант формата (арифметическое кодирование JPEG,
// 12 бит и т. п.) — ErrUnsupported, прочее — ErrCorrupt.
func decodeError(err error) error {
	var ju jpeg.UnsupportedError
	var pu png.UnsupportedError
	if errors.As(err, &ju) || errors.As(err, &pu) {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return fmt.Errorf("%w: %w", ErrCorrupt, err)
}

// walkJPEG — проход по маркерам всего файла, как его видит image/jpeg: до первого SOS — первый APP1 «Exif» (TIFF без
// префикса) и профиль из APP2 «ICC_PROFILE»; по всему файлу — первый кадр SOF0/1/2, число SOS, APP0 JFIF и APP14
// Adobe. Энтропийные данные пропускаются до следующего маркера (FF xx, кроме FF 00, заполнителей FF FF и RST) — так же,
// как их читает декодер: FF DA внутри COM/APPn сканом не считается, посреди энтропийных данных — считается (и для
// декодера это маркер).
func walkJPEG(b []byte) (exif, icc []byte, mk jpegMarkers) {
	var parts [][]byte
	for i := 2; i < len(b); {
		if b[i] != 0xFF { // мусор между сегментами и энтропийные данные декодер пропускает — разбор тоже
			j := bytes.IndexByte(b[i:], 0xFF)
			if j < 0 {
				break
			}
			i += j
		}
		for i < len(b) && b[i] == 0xFF {
			i++
		}
		if i >= len(b) {
			break
		}
		m := b[i]
		i++
		if m == 0x00 || m == 0xD8 || m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			continue
		}
		if m == 0xD9 || i+2 > len(b) {
			break
		}
		n := int(binary.BigEndian.Uint16(b[i:]))
		if n < 2 || i+n > len(b) {
			break
		}
		p := b[i+2 : i+n]
		i += n
		switch {
		case m == 0xDA:
			mk.scans++
		case (m == 0xC0 || m == 0xC1 || m == 0xC2) && mk.comps == nil:
			mk.progressive = m == 0xC2
			mk.comps = sofComps(p)
		case m == 0xE0 && len(p) >= 5:
			mk.jfif = string(p[:5]) == "JFIF\x00"
		case m == 0xEE && len(p) >= 12 && string(p[:5]) == "Adobe":
			mk.adobe, mk.adobeTransform = true, p[11]
		case mk.scans > 0:
			// метаданные после первого скана не читаются
		case m == 0xE1 && exif == nil && bytes.HasPrefix(p, []byte("Exif\x00\x00")):
			exif = p[6:]
		case m == 0xE2 && len(p) >= 14 && bytes.HasPrefix(p, []byte("ICC_PROFILE\x00")):
			parts = append(parts, p[12:])
		}
	}
	return exif, joinICC(parts), mk
}

// sofComps — компоненты кадра (идентификатор, факторы выборки); короткий сегмент — пусто (его отвергнет декодер).
func sofComps(p []byte) []jpegComp {
	if len(p) < 6 || len(p) < 6+3*int(p[5]) {
		return nil
	}
	cs := make([]jpegComp, p[5])
	for k := range cs {
		c := p[6+3*k:]
		cs[k] = jpegComp{id: int(c[0]), h: int(c[1] >> 4), v: int(c[1] & 0x0F)}
	}
	return cs
}

// decodeBytes — сколько байт выделит image/jpeg на этот файл (не JPEG — 0), по маркерам до декодирования: плоскости
// на сетке MCU (серый — 1 Б/пикс.; YCbCr — яркость и цветность по выборке, нестандартная выборка — цветность
// полная), чёрный канал у 4 компонент, картинка на выходе у CMYK/YCCK и RGB (RGBA/CMYK, 4 Б/пикс.), у прогрессивного —
// коэффициенты всех компонент (блок 8×8 — 64 × int32 = 256 Б), которые живут до конца декодирования. Формулы —
// image/jpeg Go 1.27 (scan.go makeImg, processSOS; reader.go applyBlack, convertToRGB); сверка с декодером —
// TestDecodeBytesMatchesDecoder. Кадр уже проверен DecodeConfig (факторы 1, 2, 4; делят максимум).
func decodeBytes(in info) int64 {
	cs := in.jpeg.comps
	if in.Kind != KindJPEG || len(cs) == 0 {
		return 0
	}
	w, h := int64(in.rawW), int64(in.rawH)
	hv := make([][2]int64, len(cs))
	var maxH, maxV int64 = 1, 1
	for k, c := range cs {
		hv[k] = [2]int64{int64(c.h), int64(c.v)}
		if len(cs) == 1 { // у одной компоненты MCU — один блок, факторы не важны
			hv[k] = [2]int64{1, 1}
		}
		maxH, maxV = max(maxH, hv[k][0]), max(maxV, hv[k][1])
	}
	mxx, myy := (w+8*maxH-1)/(8*maxH), (h+8*maxV-1)/(8*maxV)
	pw, ph := 8*maxH*mxx, 8*maxV*myy // плоскость яркости на сетке MCU
	total := pw * ph
	if len(cs) > 1 {
		cw, ch := pw, ph // нестандартная выборка (flex) — цветность полная
		ratio := [2]int64{maxH / hv[1][0], maxV / hv[1][1]}
		standard := ratio == [2]int64{1, 1} || ratio == [2]int64{1, 2} || ratio == [2]int64{2, 1} ||
			ratio == [2]int64{2, 2} || ratio == [2]int64{4, 1} || ratio == [2]int64{4, 2}
		if hv[1] == hv[2] && hv[0] == [2]int64{maxH, maxV} && standard {
			cw, ch = ceilDiv(pw, ratio[0]), ceilDiv(ph, ratio[1])
		}
		total += 2 * cw * ch
	}
	rgb := len(cs) == 3 && !in.jpeg.jfif &&
		(in.jpeg.adobe && in.jpeg.adobeTransform == 0 || cs[0].id == 'R' && cs[1].id == 'G' && cs[2].id == 'B')
	if len(cs) == 4 {
		total += 8 * hv[3][0] * mxx * 8 * hv[3][1] * myy // чёрный канал
	}
	if len(cs) == 4 || rgb {
		total += 4 * w * h // CMYK или RGBA на выходе, пока плоскости ещё живы
	}
	if in.jpeg.progressive {
		for _, f := range hv {
			total += mxx * myy * f[0] * f[1] * 256
		}
	}
	return total
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// joinICC склеивает многочастный профиль APP2 по номерам сегментов (порядок в файле не важен). Номера должны
// покрыть 1..N ровно по разу с одинаковым N, иначе профиля нет (он и так в выход не идёт — только распознавание).
func joinICC(parts [][]byte) []byte {
	if len(parts) == 0 || int(parts[0][1]) != len(parts) {
		return nil
	}
	ordered := make([][]byte, len(parts))
	for _, p := range parts {
		seq, total := int(p[0]), int(p[1])
		if total != len(parts) || seq < 1 || seq > total || ordered[seq-1] != nil {
			return nil
		}
		ordered[seq-1] = p[2:]
	}
	return bytes.Join(ordered, nil)
}

// walkPNG — чанки до IDAT: acTL до IDAT — APNG (после IDAT браузеры его не видят), первый iCCP.
func walkPNG(b []byte) (animated bool, iccp []byte) {
	for i := len(sigPNG); i+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		if n > len(b)-i-8 {
			break
		}
		data := b[i+8 : i+8+n]
		switch string(b[i+4 : i+8]) {
		case "IDAT":
			return animated, iccp
		case "acTL":
			animated = true
		case "iCCP":
			if iccp == nil {
				iccp = data
			}
		}
		i += 12 + n
	}
	return animated, iccp
}

// walkWebP — чанки RIFF: флаг анимации в VP8X (x/image/webp анимацию не декодирует и не сообщает о ней) и ICCP.
func walkWebP(b []byte) (animated bool, icc []byte) {
	const animationBit = 0x02
	for i := 12; i+8 <= len(b); {
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		if n > len(b)-i-8 {
			break
		}
		data := b[i+8 : i+8+n]
		switch string(b[i : i+4]) {
		case "VP8X":
			if len(data) > 0 && data[0]&animationBit != 0 {
				animated = true
			}
		case "ICCP":
			if icc == nil {
				icc = data
			}
		}
		i += 8 + n + n%2
	}
	return animated, icc
}
