package imaging

import (
	"image"
	"math"

	"golang.org/x/image/draw"
)

// targetSize — размеры варианта: длинная сторона не больше maxSide, пропорции сохраняются, увеличения нет;
// maxSide ≤ 0 — без масштаба. Поворот на 90° не меняет длинную сторону, поэтому считается по размерам до ориентации.
func targetSize(w, h, maxSide int) (int, int) {
	long := max(w, h)
	if maxSide <= 0 || long <= maxSide {
		return w, h
	}
	k := float64(maxSide) / float64(long)
	return max(1, int(math.Round(float64(w)*k))), max(1, int(math.Round(float64(h)*k)))
}

// resize — src в NRGBA w×h: CatmullRom при уменьшении, копия при том же размере (NRGBA того же размера — как есть).
func resize(src image.Image, w, h int) *image.NRGBA {
	sb := src.Bounds()
	if m, ok := src.(*image.NRGBA); ok && sb == image.Rect(0, 0, w, h) {
		return m
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	if sb.Dx() == w && sb.Dy() == h {
		draw.Draw(dst, dst.Rect, src, sb.Min, draw.Src)
		return dst
	}
	draw.CatmullRom.Scale(dst, dst.Rect, src, sb, draw.Src, nil)
	return dst
}
