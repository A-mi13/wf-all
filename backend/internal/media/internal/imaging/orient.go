package imaging

import (
	"encoding/binary"
	"image"
	"image/color"
)

// exifOrientation — тег 0x0112 (SHORT) из IFD0 блока TIFF внутри APP1 «Exif». Нет тега, мусор или значение вне
// 1–8 — 1 (без поворота: так же поступают браузеры).
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:]) != 42 {
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := range n {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 {
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}

// srcPoint — точка исходника (w×h, до ориентации), которая видна в точке (x, y) картинки после ориентации o.
func srcPoint(o, w, h, x, y int) (int, int) {
	switch o {
	case 2: // зеркало по горизонтали
		return w - 1 - x, y
	case 3: // 180°
		return w - 1 - x, h - 1 - y
	case 4: // зеркало по вертикали
		return x, h - 1 - y
	case 5: // транспонирование
		return y, x
	case 6: // 90° по часовой
		return y, h - 1 - x
	case 7: // поперечное транспонирование
		return w - 1 - y, h - 1 - x
	case 8: // 90° против часовой
		return w - 1 - y, x
	}
	return x, y
}

// orientNRGBA применяет ориентацию к готовому варианту (после уменьшения: поворот исходника 40 Мп стоил бы ещё
// одну полноразмерную копию — замер спайка).
func orientNRGBA(m *image.NRGBA, o int) *image.NRGBA {
	if o <= 1 || o > 8 {
		return m
	}
	w, h := m.Rect.Dx(), m.Rect.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := range oh {
		for x := range ow {
			sx, sy := srcPoint(o, w, h, x, y)
			s := m.PixOffset(m.Rect.Min.X+sx, m.Rect.Min.Y+sy)
			d := dst.PixOffset(x, y)
			copy(dst.Pix[d:d+4], m.Pix[s:s+4])
		}
	}
	return dst
}

// orientedView — исходник «как видит человек» без копии пикселей: его получает проверка содержимого.
type orientedView struct {
	src  image.Image
	o    int
	w, h int
}

func oriented(m image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return m
	}
	b := m.Bounds()
	return orientedView{src: m, o: o, w: b.Dx(), h: b.Dy()}
}

func (v orientedView) ColorModel() color.Model { return v.src.ColorModel() }

func (v orientedView) Bounds() image.Rectangle {
	if v.o >= 5 {
		return image.Rect(0, 0, v.h, v.w)
	}
	return image.Rect(0, 0, v.w, v.h)
}

func (v orientedView) At(x, y int) color.Color {
	if !(image.Point{x, y}.In(v.Bounds())) {
		return v.src.ColorModel().Convert(color.Transparent)
	}
	sx, sy := srcPoint(v.o, v.w, v.h, x, y)
	b := v.src.Bounds()
	return v.src.At(b.Min.X+sx, b.Min.Y+sy)
}
