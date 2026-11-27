package imaging

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
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
