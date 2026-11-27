package imaging

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"runtime"
	"testing"

	"github.com/deepteams/webp"
)

var allInputs = []Kind{KindJPEG, KindPNG, KindWebP}

var oneFull = []Spec{{Name: "full", MaxSide: 1920, Lossy: true, Quality: 80}}

// polyglot — настоящий заголовок JPEG до SOS включительно, дальше вместо энтропийных данных — HTML.
func polyglot(t testing.TB) []byte {
	t.Helper()
	j := encJPEG(t, noisy(64, 48), 90)
	i := bytes.Index(j, []byte{0xFF, 0xDA})
	if i < 0 {
		t.Fatal("нет SOS")
	}
	sos := i + 2 + int(binary.BigEndian.Uint16(j[i+2:]))
	return append(append([]byte{}, j[:sos]...),
		[]byte("<html><body><script>alert(document.cookie)</script></body></html>")...)
}

func TestSniff(t *testing.T) {
	t.Parallel()
	var gifBuf bytes.Buffer
	if err := gif.Encode(&gifBuf, gradient(8, 8), nil); err != nil {
		t.Fatal(err)
	}
	alpha := gradient(40, 30)
	alpha.Pix[3] = 10
	tests := []struct {
		name string
		in   []byte
		want Header
		err  error
	}{
		{"jpeg", encJPEG(t, noisy(64, 48), 90), Header{Kind: KindJPEG, Width: 64, Height: 48}, nil},
		{"png", encPNG(t, gradient(30, 20)), Header{Kind: KindPNG, Width: 30, Height: 20}, nil},
		{"webp lossy", encWebP(t, gradient(40, 30), webp.DefaultOptions()), Header{Kind: KindWebP, Width: 40, Height: 30}, nil},
		{"webp lossless", encWebP(t, gradient(40, 30), &webp.EncoderOptions{Lossless: true, Quality: 75, Method: 4}),
			Header{Kind: KindWebP, Width: 40, Height: 30}, nil},
		{"webp с альфой", encWebP(t, alpha, webp.DefaultOptions()), Header{Kind: KindWebP, Width: 40, Height: 30}, nil},
		{"jpeg с ориентацией 6 — размеры после поворота", withJPEGSegs(encJPEG(t, noisy(64, 48), 90), exifApp1(6)),
			Header{Kind: KindJPEG, Width: 48, Height: 64}, nil},
		{"gif", gifBuf.Bytes(), Header{}, ErrUnsupported},
		{"html", []byte("<!doctype html><html><body>hi</body></html>"), Header{}, ErrUnsupported},
		{"пустой", nil, Header{}, ErrUnsupported},
		{"3 байта", []byte{0x00, 0x01, 0x02}, Header{}, ErrUnsupported},
		{"3 байта сигнатуры JPEG без заголовка", []byte{0xFF, 0xD8, 0xFF}, Header{}, ErrCorrupt},
		{"PNG под видом JPEG — тип по содержимому", encPNG(t, gradient(10, 12)), Header{Kind: KindPNG, Width: 10, Height: 12}, nil},
		{"полиглот JPEG + HTML — заголовок JPEG", polyglot(t), Header{Kind: KindJPEG, Width: 64, Height: 48}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Sniff(tc.in)
			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
				t.Fatalf("ошибка = %v, ждали %v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("заголовок = %+v, ждали %+v", got, tc.want)
			}
		})
	}

	t.Run("полиглот JPEG + HTML не декодируется", func(t *testing.T) {
		t.Parallel()
		if _, _, err := Process(context.Background(), polyglot(t), Limits{MaxPixels: 1 << 20, Inputs: allInputs}, oneFull, nil); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("ошибка = %v, ждали ErrCorrupt", err)
		}
	})
	t.Run("PNG не из входов цели (заявлен JPEG) — отказ", func(t *testing.T) {
		t.Parallel()
		_, _, err := Process(context.Background(), encPNG(t, gradient(10, 12)), Limits{MaxPixels: 1 << 20, Inputs: []Kind{KindJPEG}}, oneFull, nil)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("ошибка = %v, ждали ErrUnsupported", err)
		}
	})
	t.Run("пустые входы цели — отказ", func(t *testing.T) {
		t.Parallel()
		_, _, err := Process(context.Background(), encJPEG(t, noisy(16, 16), 90), Limits{MaxPixels: 1 << 20}, oneFull, nil)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("ошибка = %v, ждали ErrUnsupported", err)
		}
	})
	t.Run("полиглот с HTML в комментарии — перекодирован без HTML", func(t *testing.T) {
		t.Parallel()
		in := withJPEGSegs(encJPEG(t, noisy(32, 32), 90), jpegSeg(0xFE, []byte("<script>alert(1)</script>")))
		outs, _, err := Process(context.Background(), in, Limits{MaxPixels: 1 << 20, Inputs: allInputs}, oneFull, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(outs[0].Data, []byte("<script")) {
			t.Fatal("HTML из комментария попал в вариант")
		}
	})
}

// animatedWebP — VP8X с флагом анимации, ANIM и два кадра ANMF с настоящим VP8 внутри.
func animatedWebP(t testing.TB) []byte {
	t.Helper()
	still := riffChunks(t, encWebP(t, gradient(16, 16), webp.DefaultOptions()))
	vp8 := chunkData(still, "VP8 ")
	frame := []byte{0, 0, 0, 0, 0, 0, 15, 0, 0, 15, 0, 0, 100, 0, 0, 0}
	frame = append(frame, "VP8 "...)
	frame = binary.LittleEndian.AppendUint32(frame, u32(len(vp8)))
	frame = append(frame, vp8...)
	return webpWithChunks(
		riffChunk{"VP8X", vp8x(0x02, 16, 16)},
		riffChunk{"ANIM", []byte{0, 0, 0, 0, 0, 0}},
		riffChunk{"ANMF", frame},
		riffChunk{"ANMF", frame},
	)
}

func acTL() []byte { return pngChunk("acTL", []byte{0, 0, 0, 2, 0, 0, 0, 0}) }

func TestAnimatedRejected(t *testing.T) {
	t.Parallel()
	lim := Limits{MaxPixels: 1 << 20, Inputs: allInputs}
	for name, in := range map[string][]byte{
		"анимированный WebP":  animatedWebP(t),
		"APNG (acTL до IDAT)": withPNGChunks(encPNG(t, gradient(16, 16)), acTL()),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, err := Sniff(in)
			if err != nil || !h.Animated {
				t.Fatalf("Sniff = %+v, %v; ждали Animated", h, err)
			}
			if _, _, err := Process(context.Background(), in, lim, oneFull, nil); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("ошибка = %v, ждали ErrUnsupported", err)
			}
		})
	}
	t.Run("acTL после IDAT — не анимация (браузеры его не видят)", func(t *testing.T) {
		t.Parallel()
		in := pngAfterIDAT(encPNG(t, gradient(16, 16)), acTL())
		h, err := Sniff(in)
		if err != nil || h.Animated {
			t.Fatalf("Sniff = %+v, %v; ждали неанимированный", h, err)
		}
		if _, _, err := Process(context.Background(), in, lim, oneFull, nil); err != nil {
			t.Fatal(err)
		}
	})
}

// pngBomb — PNG 1 бит серый w×h: файл в сотни байт, при декодировании — w×h байт.
func pngBomb(t testing.TB, w, h int) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, u32(w))
	ihdr = binary.BigEndian.AppendUint32(ihdr, u32(h))
	ihdr = append(ihdr, 1, 0, 0, 0, 0)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("IDAT", zlibBytes(t, make([]byte, 64)))...)
	return append(out, pngChunk("IEND", nil)...)
}

// jpegBomb — маленький JPEG с размерами в SOF0, переписанными на w×h.
func jpegBomb(t testing.TB, w, h int) []byte {
	j := encJPEG(t, gradient(16, 16), 80)
	i := bytes.Index(j, []byte{0xFF, 0xC0})
	binary.BigEndian.PutUint16(j[i+5:], u16(h))
	binary.BigEndian.PutUint16(j[i+7:], u16(w))
	return j
}

// webpBomb — VP8X с холстом w×h и ALPH (декодер x/image выделил бы w×h под альфу до проверки VP8).
func webpBomb(t testing.TB, w, h int) []byte {
	still := riffChunks(t, encWebP(t, gradient(16, 16), webp.DefaultOptions()))
	return webpWithChunks(
		riffChunk{"VP8X", vp8x(0x10, w, h)},
		riffChunk{"ALPH", []byte{0}},
		riffChunk{"VP8 ", chunkData(still, "VP8 ")},
	)
}

// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestPixelBomb(t *testing.T) {
	lim := Limits{MaxPixels: 40_000_000, Inputs: allInputs}
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"PNG 30000×30000", pngBomb(t, 30000, 30000)},
		{"JPEG 30000×30000", jpegBomb(t, 30000, 30000)},
		{"WebP 16384×16384", webpBomb(t, 16384, 16384)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.in) > 2048 {
				t.Fatalf("файл-бомба должен быть маленьким, %d байт", len(tc.in))
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, _, err := Process(context.Background(), tc.in, lim, oneFull, nil)
			runtime.ReadMemStats(&after)
			if !errors.Is(err, ErrTooManyPixels) {
				t.Fatalf("ошибка = %v, ждали ErrTooManyPixels", err)
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew >= 8<<20 {
				t.Fatalf("за вызов выделено %d байт (≥ 8 МБ): пиксели декодированы до проверки", grew)
			}
		})
	}
}

// Полоса 1×L проходит MaxPixels, но ресайз x/image/draw тратит память пропорционально длинной стороне (веса и
// буфер): длинная сторона ограничена отдельно, по заголовку.
// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestStripBomb(t *testing.T) {
	lim := Limits{MaxPixels: 40_000_000, Inputs: allInputs}
	in := encPNG(t, image.NewGray(image.Rect(0, 0, 1, 70_000)))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, err := Process(context.Background(), in, lim, threeVariants, nil)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("ошибка = %v, ждали ErrTooManyPixels", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew >= 8<<20 {
		t.Fatalf("за вызов выделено %d байт (≥ 8 МБ)", grew)
	}
	t.Run("ровно 65535 — проходит", func(t *testing.T) {
		in := encPNG(t, image.NewGray(image.Rect(0, 0, 1, 65_535)))
		if _, _, err := Process(context.Background(), in, lim, threeVariants, nil); err != nil {
			t.Fatal(err)
		}
	})
}

// Вариант без масштаба со стороной больше предела WebP (16383) — детерминированный отказ до декодирования, а не
// ошибка кодировщика с повторами задачи.
func TestVariantSideLimit(t *testing.T) {
	t.Parallel()
	in := encPNG(t, gradient(20_000, 2))
	lim := Limits{MaxPixels: 40_000_000, Inputs: allInputs}
	_, _, err := Process(context.Background(), in, lim, []Spec{{Name: "raw", MaxSide: 0, Lossy: true, Quality: 80}}, nil)
	if !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("MaxSide 0: ошибка = %v, ждали ErrTooManyPixels", err)
	}
	outs, _, err := Process(context.Background(), in, lim, []Spec{{Name: "full", MaxSide: 1920, Lossy: true, Quality: 80}}, nil)
	if err != nil || outs[0].Width != 1920 {
		t.Fatalf("MaxSide 1920: %v", err)
	}
	// граница предела WebP (Task 7 r1-m2): 16383 кодируется, 16384 — отказ до декодирования
	raw := []Spec{{Name: "raw", MaxSide: 0, Lossy: true, Quality: 80}}
	outs, _, err = Process(context.Background(), encPNG(t, gradient(webp.MaxDimension, 2)), lim, raw, nil)
	if err != nil || outs[0].Width != webp.MaxDimension {
		t.Fatalf("сторона 16383: %v", err)
	}
	if _, _, err = Process(context.Background(), encPNG(t, gradient(webp.MaxDimension+1, 2)), lim, raw, nil); !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("сторона 16384: ошибка = %v, ждали ErrTooManyPixels", err)
	}
}

func TestPixelLimitBoundary(t *testing.T) {
	t.Parallel()
	in := encPNG(t, gradient(100, 100))
	if _, _, err := Process(context.Background(), in, Limits{MaxPixels: 10_000, Inputs: allInputs}, oneFull, nil); err != nil {
		t.Fatalf("ровно на пределе: %v", err)
	}
	if _, _, err := Process(context.Background(), in, Limits{MaxPixels: 9_999, Inputs: allInputs}, oneFull, nil); !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("предел − 1: %v, ждали ErrTooManyPixels", err)
	}
}

func TestCorrupt(t *testing.T) {
	t.Parallel()
	lim := Limits{MaxPixels: 1 << 22, Inputs: allInputs}
	j := encJPEG(t, noisy(256, 256), 95)
	p := encPNG(t, noisy(64, 64))
	bomb := iccpChunk(t, make([]byte, 2<<20)) // 2 МиБ нулей: zip-бомба в профиле
	for name, in := range map[string][]byte{
		"обрезанный JPEG":              j[:len(j)*6/10],
		"обрезанный PNG":               p[:len(p)*6/10],
		"PNG с iCCP больше 1 МиБ":      withPNGChunks(encPNG(t, gradient(8, 8)), bomb),
		"PNG с iCCP — не zlib":         withPNGChunks(encPNG(t, gradient(8, 8)), pngChunk("iCCP", []byte("x\x00\x00garbage"))),
		"WebP с обрезанным VP8":        encWebP(t, noisy(64, 64), webp.DefaultOptions())[:200],
		"JPEG: длина сегмента за файл": append([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xF0}, make([]byte, 16)...),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := Process(context.Background(), in, lim, oneFull, nil); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("ошибка = %v, ждали ErrCorrupt", err)
			}
		})
	}
}

// цвет пикселя для простых проверок
func nrgbaAt(m image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
}
