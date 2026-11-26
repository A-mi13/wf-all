package imaging

import (
	"context"
	"image"
	"image/color"
	"strconv"
	"testing"
)

// Углы исходника 64×32: красный (TL), зелёный (TR), синий (BL), белый (BR).
var corner = map[string]color.NRGBA{
	"TL": {255, 0, 0, 255}, "TR": {0, 255, 0, 255}, "BL": {0, 0, 255, 255}, "BR": {255, 255, 255, 255},
}

func quadrants(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			k := map[[2]bool]string{{false, false}: "TL", {true, false}: "TR", {false, true}: "BL", {true, true}: "BR"}
			m.SetNRGBA(x, y, corner[k[[2]bool{x >= w/2, y >= h/2}]])
		}
	}
	return m
}

// nearestCorner — какой из четырёх цветов ближе (сжатие с потерями сдвигает цвет).
func nearestCorner(c color.NRGBA) string {
	best, bestD := "", 1<<30
	for k, v := range corner {
		d := sq(int(c.R)-int(v.R)) + sq(int(c.G)-int(v.G)) + sq(int(c.B)-int(v.B))
		if d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

func sq(x int) int { return x * x }

func TestOrientation(t *testing.T) {
	t.Parallel()
	const w, h = 64, 32
	src := encJPEG(t, quadrants(w, h), 95)
	// Ожидание — по смыслу тега EXIF 0x0112: какой угол исходника виден в углах TL, TR, BL, BR результата.
	tests := []struct {
		o      int
		swap   bool
		layout [4]string
	}{
		{1, false, [4]string{"TL", "TR", "BL", "BR"}},
		{2, false, [4]string{"TR", "TL", "BR", "BL"}}, // зеркало по горизонтали
		{3, false, [4]string{"BR", "BL", "TR", "TL"}}, // 180°
		{4, false, [4]string{"BL", "BR", "TL", "TR"}}, // зеркало по вертикали
		{5, true, [4]string{"TL", "BL", "TR", "BR"}},  // транспонирование
		{6, true, [4]string{"BL", "TL", "BR", "TR"}},  // 90° по часовой
		{7, true, [4]string{"BR", "TR", "BL", "TL"}},  // поперечное транспонирование
		{8, true, [4]string{"TR", "BR", "TL", "BL"}},  // 90° против часовой
	}
	for _, tc := range tests {
		t.Run(strconv.Itoa(tc.o), func(t *testing.T) {
			t.Parallel()
			in := withJPEGSegs(src, exifApp1(tc.o))
			wantW, wantH := w, h
			if tc.swap {
				wantW, wantH = h, w
			}
			var checked image.Rectangle
			check := func(m image.Image) (bool, string, error) { checked = m.Bounds(); return true, "", nil }
			outs, hdr, err := Process(context.Background(), in, Limits{MaxPixels: 1 << 20, Inputs: allInputs},
				[]Spec{{Name: "full", MaxSide: 1920, Lossy: true, Quality: 95}, {Name: "half", MaxSide: 32, Lossy: true, Quality: 95}}, check)
			if err != nil {
				t.Fatal(err)
			}
			if hdr.Width != wantW || hdr.Height != wantH {
				t.Fatalf("заголовок %d×%d, ждали %d×%d", hdr.Width, hdr.Height, wantW, wantH)
			}
			if checked.Dx() != wantW || checked.Dy() != wantH {
				t.Fatalf("проверка содержимого получила %v, ждали %d×%d", checked, wantW, wantH)
			}
			full := outs[0]
			if full.Width != wantW || full.Height != wantH {
				t.Fatalf("вариант %d×%d, ждали %d×%d", full.Width, full.Height, wantW, wantH)
			}
			if half := outs[1]; half.Width != wantW/2 || half.Height != wantH/2 {
				t.Fatalf("уменьшенный вариант %d×%d, ждали %d×%d", half.Width, half.Height, wantW/2, wantH/2)
			}
			for i, o := range outs {
				m := decodeOut(t, o.Data)
				b := m.Bounds()
				if b.Dx() != o.Width || b.Dy() != o.Height {
					t.Fatalf("%s: размеры WebP %v не равны Output %d×%d", o.Name, b, o.Width, o.Height)
				}
				pts := [4]image.Point{{2, 2}, {b.Dx() - 3, 2}, {2, b.Dy() - 3}, {b.Dx() - 3, b.Dy() - 3}}
				for j, p := range pts {
					if got := nearestCorner(nrgbaAt(m, p.X, p.Y)); got != tc.layout[j] {
						t.Fatalf("вариант %d, угол %d: цвет угла %s исходника, ждали %s", i, j, got, tc.layout[j])
					}
				}
			}
		})
	}
	t.Run("ориентация вне 1–8 — как 1", func(t *testing.T) {
		t.Parallel()
		_, hdr, err := Process(context.Background(), withJPEGSegs(src, exifApp1(9)), Limits{MaxPixels: 1 << 20, Inputs: allInputs}, oneFull, nil)
		if err != nil || hdr.Width != w || hdr.Height != h {
			t.Fatalf("%+v, %v", hdr, err)
		}
	})
}
