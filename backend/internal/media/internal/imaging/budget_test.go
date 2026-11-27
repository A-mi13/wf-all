package imaging

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"image/png"
	"runtime"
	"testing"
)

// allocDuring — сколько байт выделил процесс за f (TotalAlloc — счётчик процесса: вызывающий тест — serial).
func allocDuring(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// beforeEOI вставляет сегменты перед EOI (после всех сканов).
func beforeEOI(j []byte, segs ...[]byte) []byte {
	out := append([]byte{}, j[:len(j)-2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, 0xFF, 0xD9)
}

// adobeRGB — APP14 Adobe с transform 0: три компоненты image/jpeg читает как RGB и переводит в RGBA (4 Б/пикс.).
var adobeRGB = jpegSeg(0xEE, []byte("Adobe\x00\x64\x00\x00\x00\x00\x00"))

// Финальное ревью media, D-C2: прогрессивный 4:4:4/CMYK в несколько сотен байт поднимал кучу до 1–1,2 ГиБ (буфер
// коэффициентов 256 Б на блок каждой компоненты). Бюджет декодера в байтах проверяется до декодирования.
// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestProgressiveCoeffBomb(t *testing.T) {
	lim := Limits{MaxPixels: 40_000_000, Inputs: allInputs}
	for _, tc := range []struct {
		name string
		hv   []byte
	}{
		{"CMYK 4:4:4", []byte{0x11, 0x11, 0x11, 0x11}},
		{"YCbCr 4:4:4", []byte{0x11, 0x11, 0x11}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := zeroJPEG(7744, 5164, tc.hv, true)
			if len(in) > 2048 {
				t.Fatalf("файл-бомба должен быть маленьким, %d байт", len(in))
			}
			var err error
			grew := allocDuring(func() { _, _, err = Process(context.Background(), in, lim, threeVariants, nil) })
			if !errors.Is(err, ErrTooManyPixels) {
				t.Fatalf("ошибка = %v, ждали ErrTooManyPixels", err)
			}
			if grew >= 8<<20 {
				t.Fatalf("за вызов выделено %d байт (≥ 8 МБ): декодер запущен до проверки бюджета", grew)
			}
		})
	}
}

// Граница бюджета (8 Б/пикс. × MaxPixels): прогрессивный 4:4:4 64×64 — 3 плоскости + 3 × 256 Б на блок = 15 Б/пикс. =
// 61440 Б; MaxPixels 7680 даёт ровно 61440 — проходит, 7679 — отказ. Обычные снимки — внутри бюджета на пределе пикселей.
func TestDecodeBudgetBoundary(t *testing.T) {
	t.Parallel()
	prog444 := zeroJPEG(64, 64, []byte{0x11, 0x11, 0x11}, true)
	for _, tc := range []struct {
		name string
		in   []byte
		max  int64
		ok   bool
	}{
		{"прогрессивный 4:4:4 ровно на бюджете", prog444, 7680, true},
		{"прогрессивный 4:4:4 бюджет − 8 Б", prog444, 7679, false},
		{"прогрессивный 4:2:0 на пределе пикселей (7,5 Б/пикс.)", zeroJPEG(1024, 1024, []byte{0x22, 0x11, 0x11}, true), 1 << 20, true},
		{"базовый CMYK на пределе пикселей (8 Б/пикс.)", zeroJPEG(1024, 1024, []byte{0x11, 0x11, 0x11, 0x11}, false), 1 << 20, true},
		{"базовый 4:2:0 (Go-кодировщик) на пределе пикселей", encJPEG(t, noisy(1024, 768), 90), 1024 * 768, true},
		{"прогрессивный 4:2:2 на пределе пикселей (10 Б/пикс.)", zeroJPEG(1024, 1024, []byte{0x21, 0x11, 0x11}, true), 1 << 20, false},
		{"прогрессивный RGB (APP14 после сканов): + RGBA", beforeEOI(zeroJPEG(1024, 1024, []byte{0x22, 0x11, 0x11}, true), adobeRGB), 1 << 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := Process(context.Background(), tc.in, Limits{MaxPixels: tc.max, Inputs: allInputs}, oneFull, nil)
			if tc.ok && err != nil {
				t.Fatalf("ошибка = %v, ждали успех", err)
			}
			if !tc.ok && !errors.Is(err, ErrTooManyPixels) {
				t.Fatalf("ошибка = %v, ждали ErrTooManyPixels", err)
			}
		})
	}
}

// Оценка бюджета совпадает с тем, что на самом деле выделяет image/jpeg (плоскости на сетке MCU, чёрный канал,
// RGBA/CMYK на выходе, коэффициенты прогрессивного): заниженная оценка пропустила бы бомбу.
// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestDecodeBytesMatchesDecoder(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"серый базовый", zeroJPEG(1000, 700, []byte{0x11}, false)},
		{"серый прогрессивный", zeroJPEG(1000, 700, []byte{0x11}, true)},
		{"4:2:0 базовый", zeroJPEG(1000, 700, []byte{0x22, 0x11, 0x11}, false)},
		{"4:2:0 прогрессивный", zeroJPEG(1000, 700, []byte{0x22, 0x11, 0x11}, true)},
		{"4:2:2 прогрессивный", zeroJPEG(1000, 700, []byte{0x21, 0x11, 0x11}, true)},
		{"4:4:4 прогрессивный", zeroJPEG(1000, 700, []byte{0x11, 0x11, 0x11}, true)},
		{"нестандартная выборка (flex)", zeroJPEG(1000, 700, []byte{0x12, 0x21, 0x11}, false)},
		{"CMYK базовый", zeroJPEG(1000, 700, []byte{0x11, 0x11, 0x11, 0x11}, false)},
		{"CMYK 2×2 прогрессивный", zeroJPEG(1000, 700, []byte{0x22, 0x11, 0x11, 0x22}, true)},
		{"RGB (APP14 transform 0)", withJPEGSegs(zeroJPEG(1000, 700, []byte{0x11, 0x11, 0x11}, false), adobeRGB)},
		{"RGB прогрессивный, APP14 после сканов", beforeEOI(zeroJPEG(1000, 700, []byte{0x22, 0x11, 0x11}, true), adobeRGB)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := inspect(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			est := decodeBytes(in)
			var derr error
			got := allocDuring(func() { _, derr = jpeg.Decode(bytes.NewReader(tc.in)) })
			if derr != nil {
				t.Fatalf("декодер: %v", derr)
			}
			// сверх оценки — только мелочь декодера (таблицы, буфер чтения): ≤ 64 КиБ
			if g := float64(got); g < float64(est) || g > float64(est+64<<10) { // float64: точен до 2^53, без G115
				t.Fatalf("оценка %d Б, декодер выделил %d Б", est, got)
			}
		})
	}
}

// Перепроверка финального ревью media, W1: PNG 16 бит Adam7 на 40 Мп (файл в сотни КБ) держит итог и проход разом —
// ≈ 12 Б/пикс., два таких вызова поднимали память до 1,84 ГиБ. PNG — в том же байтовом бюджете, что JPEG: отказ до
// декодирования.
// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestPNGAdam7Bomb(t *testing.T) {
	lim := Limits{MaxPixels: 40_000_000, Inputs: allInputs}
	in := zeroPNG(t, 7744, 5164, 16, 6, true)
	if len(in) > 1<<20 {
		t.Fatalf("файл-бомба должен быть маленьким, %d байт", len(in))
	}
	var err error
	grew := allocDuring(func() { _, _, err = Process(context.Background(), in, lim, threeVariants, nil) })
	if !errors.Is(err, ErrTooManyPixels) {
		t.Fatalf("ошибка = %v, ждали ErrTooManyPixels", err)
	}
	if grew >= 8<<20 {
		t.Fatalf("за вызов выделено %d байт (≥ 8 МБ): декодер запущен до проверки бюджета", grew)
	}
}

// Граница бюджета у PNG: 16 бит RGBA Adam7 64×64 — итог 64×64×8 + наибольший проход (7-й, 64×32) × 8 = 49152 Б;
// MaxPixels 6144 даёт ровно 49152 — проходит, 6143 — отказ. Обычные PNG проходят на пределе пикселей.
func TestPNGDecodeBudgetBoundary(t *testing.T) {
	t.Parallel()
	adam7 := zeroPNG(t, 64, 64, 16, 6, true)
	for _, tc := range []struct {
		name string
		in   []byte
		max  int64
		ok   bool
	}{
		{"16 бит Adam7 ровно на бюджете", adam7, 6144, true},
		{"16 бит Adam7 бюджет − 8 Б", adam7, 6143, false},
		{"16 бит Adam7 на пределе пикселей (12 Б/пикс.)", adam7, 64 * 64, false},
		{"16 бит RGBA на пределе пикселей (8 Б/пикс.)", zeroPNG(t, 1024, 1024, 16, 6, false), 1 << 20, true},
		{"8 бит RGBA Adam7 на пределе пикселей (6 Б/пикс.)", zeroPNG(t, 1024, 1024, 8, 6, true), 1 << 20, true},
		{"скриншот: 8 бит RGBA Go-кодировщика на пределе пикселей", encPNG(t, noisy(1024, 768)), 1024 * 768, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := Process(context.Background(), tc.in, Limits{MaxPixels: tc.max, Inputs: allInputs}, oneFull, nil)
			if tc.ok && err != nil {
				t.Fatalf("ошибка = %v, ждали успех", err)
			}
			if !tc.ok && !errors.Is(err, ErrTooManyPixels) {
				t.Fatalf("ошибка = %v, ждали ErrTooManyPixels", err)
			}
		})
	}
}

// Оценка бюджета PNG совпадает с выделениями image/png: тип картинки по типу цвета, глубине и tRNS (серый и RGB с
// tRNS — NRGBA/NRGBA64), палитра — 1 Б/пикс. У Adam7 оценка — итог и наибольший проход (живут разом); декодер
// выделяет ещё остальные проходы (мусор к моменту следующего), всего ≈ 2 итога — не больше оценки × 4/3.
// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestPNGDecodeBytesMatchesDecoder(t *testing.T) {
	trns2, trns6 := pngChunk("tRNS", []byte{0, 0}), pngChunk("tRNS", make([]byte, 6))
	for _, tc := range []struct {
		name      string
		in        []byte
		interlace bool
	}{
		{"серый 1 бит", zeroPNG(t, 1000, 700, 1, 0, false), false},
		{"серый 8 бит", zeroPNG(t, 1000, 700, 8, 0, false), false},
		{"серый 8 бит с tRNS", zeroPNG(t, 1000, 700, 8, 0, false, trns2), false},
		{"серый 16 бит", zeroPNG(t, 1000, 700, 16, 0, false), false},
		{"серый 16 бит с tRNS", zeroPNG(t, 1000, 700, 16, 0, false, trns2), false},
		{"палитра 8 бит с tRNS", zeroPNG(t, 1000, 700, 8, 3, false, pngChunk("tRNS", []byte{0})), false},
		{"RGB 8 бит", zeroPNG(t, 1000, 700, 8, 2, false), false},
		{"RGB 8 бит с tRNS", zeroPNG(t, 1000, 700, 8, 2, false, trns6), false},
		{"серый с альфой 8 бит", zeroPNG(t, 1000, 700, 8, 4, false), false},
		{"RGBA 8 бит", zeroPNG(t, 1000, 700, 8, 6, false), false},
		{"RGB 16 бит", zeroPNG(t, 1000, 700, 16, 2, false), false},
		{"RGBA 16 бит", zeroPNG(t, 1000, 700, 16, 6, false), false},
		{"RGBA 16 бит Adam7", zeroPNG(t, 1000, 700, 16, 6, true), true},
		{"серый 8 бит Adam7", zeroPNG(t, 1000, 700, 8, 0, true), true},
		{"палитра 4 бита Adam7", zeroPNG(t, 1000, 700, 4, 3, true), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := inspect(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			est := decodeBytes(in)
			var derr error
			got := allocDuring(func() { _, derr = png.Decode(bytes.NewReader(tc.in)) })
			if derr != nil {
				t.Fatalf("декодер: %v", derr)
			}
			hi := est + 128<<10 // сверх оценки — zlib и буферы строк
			if tc.interlace {
				hi = est*4/3 + 128<<10
			}
			if g := float64(got); g < float64(est) || g > float64(hi) { // float64: точен до 2^53, без G115
				t.Fatalf("оценка %d Б, декодер выделил %d Б", est, got)
			}
		})
	}
}

// Финальное ревью media, D-I1: каждый скан прогрессивного JPEG — обход всех блоков компоненты, скан из нулевых
// коэффициентов стоит десятки байт, а срок задачи синхронный декодер не прерывает. Больше 100 сканов — отказ до
// декодирования. Сегменты между сканами (COM с байтами FF DA) сканами не считаются.
// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestScanLimit(t *testing.T) {
	lim := Limits{MaxPixels: 40_000_000, Inputs: allInputs}
	t.Run("101 скан на 40 Мп — отказ до декодирования", func(t *testing.T) {
		in := progScans(7744, 5164, 101)
		var err error
		grew := allocDuring(func() { _, _, err = Process(context.Background(), in, lim, oneFull, nil) })
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("ошибка = %v, ждали ErrUnsupported", err)
		}
		if grew >= 8<<20 {
			t.Fatalf("за вызов выделено %d байт (≥ 8 МБ): декодер запущен до проверки", grew)
		}
	})
	t.Run("ровно 100 — проходит", func(t *testing.T) {
		if _, _, err := Process(context.Background(), progScans(64, 64, 100), lim, oneFull, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("FF DA внутри COM между сканами — не скан", func(t *testing.T) {
		com := jpegSeg(0xFE, bytes.Repeat([]byte{0xFF, 0xDA, 0x00, 0x08}, 8))
		in := progScans(64, 64, 100)
		at := bytes.LastIndex(in, []byte{0xFF, 0xDA})
		in = append(append(append([]byte{}, in[:at]...), com...), in[at:]...)
		if _, _, err := Process(context.Background(), in, lim, oneFull, nil); err != nil {
			t.Fatal(err)
		}
	})
}

// Ревью R1, m3: tRNS меняет тип картинки только там, где декодер его принимает (серый — 2 байта, RGB — 6; у палитры
// тип не меняется). Недопустимый tRNS не раздувает оценку: битый файл — corrupt от декодера, а не too_many_pixels.
// Серый 16 бит Adam7 64×64: без tRNS 3 Б/пикс. (в бюджете 8), с законным tRNS — 12 (отказ).
func TestPNGTRNSOnlyWhereLegal(t *testing.T) {
	t.Parallel()
	lim := Limits{MaxPixels: 64 * 64, Inputs: allInputs}
	for _, tc := range []struct {
		name string
		in   []byte
		want error
	}{
		{"серый 16 бит Adam7, tRNS 6 байт — битый", zeroPNG(t, 64, 64, 16, 0, true, pngChunk("tRNS", make([]byte, 6))), ErrCorrupt},
		{"серый 16 бит Adam7, tRNS 2 байта — NRGBA64", zeroPNG(t, 64, 64, 16, 0, true, pngChunk("tRNS", make([]byte, 2))), ErrTooManyPixels},
		{"RGBA 8 бит с tRNS — битый", zeroPNG(t, 64, 64, 8, 6, false, pngChunk("tRNS", make([]byte, 6))), ErrCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := Process(context.Background(), tc.in, lim, oneFull, nil); !errors.Is(err, tc.want) {
				t.Fatalf("ошибка = %v, ждали %v", err, tc.want)
			}
		})
	}
}
