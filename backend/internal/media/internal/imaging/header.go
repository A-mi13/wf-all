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
}

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
		exif, in.icc = walkJPEG(b)
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

// walkJPEG — сегменты до SOS: первый APP1 «Exif» (TIFF без префикса) и профиль из APP2 «ICC_PROFILE».
func walkJPEG(b []byte) (exif, icc []byte) {
	var parts [][]byte
	for i := 2; i < len(b); {
		if b[i] != 0xFF { // мусор между сегментами декодер пропускает — разбор тоже
			i++
			continue
		}
		for i < len(b) && b[i] == 0xFF {
			i++
		}
		if i >= len(b) {
			break
		}
		m := b[i]
		i++
		if m == 0xD8 || m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			continue
		}
		if m == 0xDA || m == 0xD9 || i+2 > len(b) {
			break
		}
		n := int(binary.BigEndian.Uint16(b[i:]))
		if n < 2 || i+n > len(b) {
			break
		}
		p := b[i+2 : i+n]
		i += n
		switch {
		case m == 0xE1 && exif == nil && bytes.HasPrefix(p, []byte("Exif\x00\x00")):
			exif = p[6:]
		case m == 0xE2 && len(p) >= 14 && bytes.HasPrefix(p, []byte("ICC_PROFILE\x00")):
			parts = append(parts, p[12:])
		}
	}
	return exif, joinICC(parts)
}

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
