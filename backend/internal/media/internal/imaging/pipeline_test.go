package imaging

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"sync"
	"testing"

	"github.com/deepteams/webp"
)

var threeVariants = []Spec{
	{Name: "thumb", MaxSide: 320, Lossy: true, Quality: 80},
	{Name: "card", MaxSide: 960, Lossy: true, Quality: 80},
	{Name: "full", MaxSide: 1920, Lossy: true, Quality: 80},
}

func TestMetadataStripped(t *testing.T) {
	t.Parallel()
	base := noisy(48, 32)
	jpg := withJPEGSegs(encJPEG(t, base, 90),
		jpegSeg(0xE1, append([]byte("Exif\x00\x00"), gpsExif()...)),
		jpegSeg(0xE1, append([]byte(xmpNS+"\x00"), xmpPacket()...)),
		jpegSeg(0xFE, []byte("GPS comment WF-SECRET-MARK")),
		jpegSeg(0xED, []byte("Photoshop 3.0\x00 GPS WF-SECRET-MARK")),
	)
	pngIn := withPNGChunks(encPNG(t, base),
		pngChunk("eXIf", gpsExif()),
		pngChunk("tEXt", []byte("Comment\x00GPS WF-SECRET-MARK")),
		pngChunk("iTXt", append([]byte("XML:com.adobe.xmp\x00\x00\x00\x00\x00"), xmpPacket()...)),
	)
	webpIn := encWebP(t, base, &webp.EncoderOptions{Quality: 80, Method: 4, EXIF: gpsExif(), XMP: xmpPacket()})
	if !bytes.Contains(webpIn, []byte("WF-SECRET-MARK")) {
		t.Fatal("тестовый WebP без метаданных — тест вакуумен")
	}
	for name, in := range map[string][]byte{"JPEG": jpg, "PNG": pngIn, "WebP": webpIn} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			outs, _, err := Process(context.Background(), in, Limits{MaxPixels: 1 << 20, Inputs: allInputs},
				append(threeVariants, Spec{Name: "lossless", MaxSide: 1920, Quality: 75}), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(outs) != 4 {
				t.Fatalf("вариантов %d, ждали 4", len(outs))
			}
			for _, o := range outs {
				for _, c := range riffChunks(t, o.Data) {
					if c.id == "EXIF" || c.id == "XMP " {
						t.Fatalf("%s: чанк %q в варианте", o.Name, c.id)
					}
					if c.id == "VP8X" && c.data[0]&0x0C != 0 {
						t.Fatalf("%s: флаги EXIF/XMP в VP8X", o.Name)
					}
				}
				for _, m := range []string{"GPS", "http://ns.adobe.com/xap", "WF-SECRET-MARK", "Exif"} {
					if bytes.Contains(o.Data, []byte(m)) {
						t.Fatalf("%s: в варианте строка %q", o.Name, m)
					}
				}
			}
		})
	}
}

func TestAlphaPreserved(t *testing.T) {
	t.Parallel()
	src := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			src.SetNRGBA(x, y, color.NRGBA{uint8(x * 4), 100, uint8(y * 4), uint8((x + y) * 2)})
		}
	}
	for name, in := range map[string][]byte{
		"PNG":  encPNG(t, src),
		"WebP": encWebP(t, src, &webp.EncoderOptions{Lossless: true, Quality: 75, Method: 4, Exact: true}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			outs, _, err := Process(context.Background(), in, Limits{MaxPixels: 1 << 20, Inputs: allInputs}, oneFull, nil)
			if err != nil {
				t.Fatal(err)
			}
			m := decodeOut(t, outs[0].Data)
			if _, ok := m.(*image.NYCbCrA); !ok {
				t.Fatalf("выход без альфы: %T", m)
			}
			for y := range 64 {
				for x := range 64 {
					got, want := int(nrgbaAt(m, x, y).A), int(src.NRGBAAt(x, y).A)
					if got-want > 2 || want-got > 2 {
						t.Fatalf("альфа (%d,%d) = %d, ждали %d ±2", x, y, got, want)
					}
				}
			}
		})
	}
	t.Run("JPEG — без альфы", func(t *testing.T) {
		t.Parallel()
		outs, _, err := Process(context.Background(), encJPEG(t, noisy(32, 32), 90), Limits{MaxPixels: 1 << 20, Inputs: allInputs}, oneFull, nil)
		if err != nil {
			t.Fatal(err)
		}
		if chunkData(riffChunks(t, outs[0].Data), "ALPH") != nil {
			t.Fatal("у непрозрачного входа появился ALPH")
		}
	})
}

func TestNoUpscale(t *testing.T) {
	t.Parallel()
	outs, hdr, err := Process(context.Background(), encPNG(t, gradient(300, 200)), Limits{MaxPixels: 1 << 20, Inputs: allInputs},
		[]Spec{{Name: "full", MaxSide: 1920, Lossy: true, Quality: 80}, {Name: "raw", MaxSide: 0, Lossy: true, Quality: 80}, {Name: "edge", MaxSide: 300, Lossy: true, Quality: 80}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hdr != (Header{Kind: KindPNG, Width: 300, Height: 200}) {
		t.Fatalf("заголовок %+v", hdr)
	}
	for _, o := range outs {
		if o.Width != 300 || o.Height != 200 {
			t.Fatalf("%s: %d×%d, ждали 300×200 без увеличения", o.Name, o.Width, o.Height)
		}
		if b := decodeOut(t, o.Data).Bounds(); b.Dx() != 300 || b.Dy() != 200 {
			t.Fatalf("%s: WebP %v", o.Name, b)
		}
	}
}

func TestVariantsSizes(t *testing.T) {
	t.Parallel()
	in := encJPEG(t, gradient(4000, 3000), 85)
	// порядок вариантов в выходе — как в запросе, даже если запрошены не по убыванию
	specs := []Spec{threeVariants[1], threeVariants[0], threeVariants[2]}
	outs, _, err := Process(context.Background(), in, Limits{MaxPixels: 40_000_000, Inputs: allInputs}, specs, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]int{"thumb": {320, 240}, "card": {960, 720}, "full": {1920, 1440}}
	for i, o := range outs {
		if o.Name != specs[i].Name || o.ContentType != "image/webp" {
			t.Fatalf("вариант %d: %q %q", i, o.Name, o.ContentType)
		}
		w := want[o.Name]
		if o.Width != w[0] || o.Height != w[1] {
			t.Fatalf("%s: %d×%d, ждали %d×%d", o.Name, o.Width, o.Height, w[0], w[1])
		}
		if b := decodeOut(t, o.Data).Bounds(); b.Dx() != w[0] || b.Dy() != w[1] {
			t.Fatalf("%s: WebP %v", o.Name, b)
		}
	}
}

func TestContentCheckCalled(t *testing.T) {
	t.Parallel()
	in := encJPEG(t, noisy(13, 13), 90)
	lim := Limits{MaxPixels: 1 << 20, Inputs: allInputs}
	t.Run("отказ с категорией", func(t *testing.T) {
		t.Parallel()
		calls := 0
		outs, _, err := Process(context.Background(), in, lim, oneFull, func(image.Image) (bool, string, error) {
			calls++
			return false, "test_marker", nil
		})
		if !errors.Is(err, ErrContentRejected) {
			t.Fatalf("ошибка = %v, ждали ErrContentRejected", err)
		}
		var rej *ContentRejectedError
		if !errors.As(err, &rej) || rej.Category != "test_marker" {
			t.Fatalf("категория: %#v", rej)
		}
		if calls != 1 || outs != nil {
			t.Fatalf("вызовов %d, вариантов %d", calls, len(outs))
		}
	})
	t.Run("сбой проверки — ошибка, не отказ", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("сервис проверки недоступен")
		_, _, err := Process(context.Background(), in, lim, oneFull, func(image.Image) (bool, string, error) { return false, "", boom })
		if !errors.Is(err, boom) || errors.Is(err, ErrContentRejected) {
			t.Fatalf("ошибка = %v", err)
		}
	})
	t.Run("разрешено — варианты есть", func(t *testing.T) {
		t.Parallel()
		outs, _, err := Process(context.Background(), in, lim, oneFull, func(m image.Image) (bool, string, error) {
			if m.Bounds().Dx() != 13 {
				t.Errorf("проверке передано %v", m.Bounds())
			}
			return true, "", nil
		})
		if err != nil || len(outs) != 1 {
			t.Fatalf("%d, %v", len(outs), err)
		}
	})
	t.Run("check == nil — без проверки", func(t *testing.T) {
		t.Parallel()
		if outs, _, err := Process(context.Background(), in, lim, oneFull, nil); err != nil || len(outs) != 1 {
			t.Fatalf("%d, %v", len(outs), err)
		}
	})
}

func TestProcessCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Process(ctx, encJPEG(t, noisy(32, 32), 90), Limits{MaxPixels: 1 << 20, Inputs: allInputs}, oneFull, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v, ждали context.Canceled", err)
	}
	for _, rej := range []error{ErrUnsupported, ErrTooManyPixels, ErrCorrupt, ErrContentRejected} {
		if errors.Is(err, rej) {
			t.Fatalf("отмена выдана за отказ %v", rej)
		}
	}
}

// Повтор задачи и два исполнителя (§3.3 «одинаковые байты»): параллельные вызовы над одним входом дают те же байты.
func TestProcessDeterministicConcurrent(t *testing.T) {
	t.Parallel()
	in := withPNGChunks(encPNG(t, noisy(200, 150)), iccpChunk(t, buildICC(iccOpts{desc: "Display P3", prim: appleP3})))
	start := make(chan struct{})
	var wg sync.WaitGroup
	res := make([][]Output, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res[i], _, errs[i] = Process(context.Background(), in, Limits{MaxPixels: 1 << 20, Inputs: allInputs}, threeVariants, nil)
		}()
	}
	close(start)
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
	}
	for k := range res[0] {
		if !bytes.Equal(res[0][k].Data, res[1][k].Data) {
			t.Fatalf("вариант %s различается между вызовами", res[0][k].Name)
		}
	}
}
