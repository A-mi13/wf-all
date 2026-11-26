package imaging

import (
	"context"
	"image"
	"image/color"
	"runtime"
	"runtime/metrics"
	"sync"
	"testing"
	"time"
)

// photo40 — худший JPEG спайка (spike.md §2): 7744×5164 (39,99 Мп), градиент + шум на сетке 6 px ±40 + шум
// пикселя ±10, q90 — детали переживают уменьшение, кодировщику тяжело.
func photo40(b *testing.B) []byte {
	const w, h = 7744, 5164
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	grid := make([]int, (w/6+1)*(h/6+1))
	for i := range grid {
		grid[i] = hashNoise(i, 1)*80/255 - 40
	}
	clamp := func(v int) uint8 { return u8(min(255, max(0, v))) }
	for y := range h {
		for x := range w {
			g := grid[(y/6)*(w/6+1)+x/6]
			n := hashNoise(x, y)*20/255 - 10
			m.SetNRGBA(x, y, color.NRGBA{clamp(x*255/w + g + n), clamp(y*255/h + g - n), clamp(128 + g), 255})
		}
	}
	return encJPEG(b, m, 90)
}

// BenchmarkProcess40MP — ручной замер (в CI не гоняется: бенчмарки идут только с -bench):
//
//	go test ./internal/media/internal/imaging/ -run '^$' -bench Process40MP -benchtime 3x -count=1
//
// Отчёт: время на вызов и пик кучи (runtime/metrics, опрос раз в 2 мс) — сверка с замером спайка (Task 1).
func BenchmarkProcess40MP(b *testing.B) {
	in := photo40(b)
	runtime.GC() // пиксели генерации — мусор до начала замера
	lim := Limits{MaxPixels: 40_000_000, Inputs: allInputs}
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	var (
		peak uint64
		mu   sync.Mutex
	)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(2 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				metrics.Read(sample)
				mu.Lock()
				peak = max(peak, sample[0].Value.Uint64())
				mu.Unlock()
			}
		}
	}()
	b.ResetTimer()
	for range b.N {
		// пик — одного вызова: мусор прошлого и генерации входа убирается вне замера
		b.StopTimer()
		runtime.GC()
		b.StartTimer()
		if _, _, err := Process(context.Background(), in, lim, threeVariants, nil); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	close(stop)
	<-done
	mu.Lock()
	defer mu.Unlock()
	b.ReportMetric(float64(peak)/(1<<20), "peakHeapMiB")
}
