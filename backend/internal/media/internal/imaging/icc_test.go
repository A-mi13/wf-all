package imaging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"github.com/deepteams/webp"
)

// SHA-256 эталона из отчёта спайка (spike.md §3) — сверка, что вшит именно первоисточник.
const refSHA = "cb51de38e482ee974c0c76b9689e16aad04bad16e226fed2f30c842d15ff3a3d"

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestReferenceProfile(t *testing.T) {
	t.Parallel()
	if got := sha(referenceP3); got != refSHA || len(referenceP3) != 480 {
		t.Fatalf("эталон: %d байт, sha256 %s", len(referenceP3), got)
	}
}

func TestIsDisplayP3(t *testing.T) {
	t.Parallel()
	changed := append([]byte{}, referenceP3...)
	changed[len(changed)-1] ^= 0xFF // эталон, но не байт в байт; desc у него «sP3»
	near := appleP3
	near[2][2] += 0.0015
	far := appleP3
	far[2][2] += 0.003
	tests := []struct {
		name string
		p    []byte
		want bool
	}{
		{"эталон байт в байт (desc sP3)", referenceP3, true},
		{"эталон с изменённым байтом", changed, false},
		{"профиль Apple Display P3 (mluc)", buildICC(iccOpts{desc: "Display P3", prim: appleP3}), true},
		{"первичные эталона, desc Display P3", buildICC(iccOpts{desc: "Display P3", prim: refP3}), true},
		{"Display P3 в v2 textDescription", buildICC(iccOpts{desc: "Display P3", v2: true, prim: appleP3}), true},
		{"регистр desc не важен", buildICC(iccOpts{desc: "my DISPLAY p3 profile", prim: appleP3}), true},
		{"координаты в допуске ±0.002", buildICC(iccOpts{desc: "Display P3", prim: near}), true},
		{"координаты вне допуска", buildICC(iccOpts{desc: "Display P3", prim: far}), false},
		{"«под P3»: desc Display P3, координаты sRGB", buildICC(iccOpts{desc: "Display P3", prim: srgb}), false},
		{"координаты P3, desc sRGB", buildICC(iccOpts{desc: "sRGB IEC61966-2.1", prim: appleP3}), false},
		{"обрезанный профиль", buildICC(iccOpts{desc: "Display P3", prim: appleP3})[:140], false},
		{"пусто", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isDisplayP3(tc.p); got != tc.want {
				t.Fatalf("isDisplayP3 = %v, ждали %v", got, tc.want)
			}
		})
	}
}

// noSubstring — ни одна 32-байтная подстрока профиля пользователя не встречается в выходе.
func noSubstring(t *testing.T, out, profile []byte) {
	t.Helper()
	for i := 0; i+32 <= len(profile); i++ {
		if bytes.Contains(out, profile[i:i+32]) {
			t.Fatalf("байты профиля пользователя [%d:%d] попали в вариант", i, i+32)
		}
	}
}

func TestICCNotCopied(t *testing.T) {
	t.Parallel()
	fakeP3 := buildICC(iccOpts{desc: "Display P3", prim: srgb})
	apple := buildICC(iccOpts{desc: "Display P3", prim: appleP3})
	srgbP := buildICC(iccOpts{desc: "sRGB", prim: srgb})
	base := noisy(48, 32)
	j := encJPEG(t, base, 90)
	p := encPNG(t, base)
	// наш же вариант с эталоном (перезагрузка скачанной картинки) — должен снова получить эталон
	ownVariant := encWebP(t, base, &webp.EncoderOptions{Quality: 80, Method: 4, ICC: referenceP3})
	foreignWebP := encWebP(t, base, &webp.EncoderOptions{Quality: 80, Method: 4, ICC: fakeP3})
	tests := []struct {
		name    string
		in      []byte
		profile []byte // профиль пользователя во входе
		wantRef bool
	}{
		{"JPEG «под P3»", withJPEGSegs(j, jpegICC(fakeP3, 1, false)...), fakeP3, false},
		{"PNG «под P3»", withPNGChunks(p, iccpChunk(t, fakeP3)), fakeP3, false},
		{"WebP «под P3»", foreignWebP, fakeP3, false},
		{"JPEG sRGB", withJPEGSegs(j, jpegICC(srgbP, 1, false)...), srgbP, false},
		{"JPEG без профиля", j, nil, false},
		{"JPEG P3 Apple, 3 сегмента APP2 в обратном порядке", withJPEGSegs(j, jpegICC(apple, 3, true)...), apple, true},
		{"PNG P3 Apple", withPNGChunks(p, iccpChunk(t, apple)), apple, true},
		{"WebP с эталоном (наш вариант)", ownVariant, nil, true},
		{"JPEG P3: сегменты APP2 с дублем номера — профиля нет", withJPEGSegs(j, append(jpegICC(apple, 2, false), jpegICC(apple, 2, false)[0])...), apple, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			outs, _, err := Process(context.Background(), tc.in, Limits{MaxPixels: 1 << 20, Inputs: allInputs},
				[]Spec{{Name: "full", MaxSide: 1920, Lossy: true, Quality: 80}, {Name: "thumb", MaxSide: 16, Lossy: false, Quality: 75}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range outs {
				cs := riffChunks(t, o.Data)
				icc := chunkData(cs, "ICCP")
				if tc.wantRef {
					if sha(icc) != refSHA {
						t.Fatalf("%s: ICCP %d байт, sha %s — ждали эталон", o.Name, len(icc), sha(icc))
					}
					continue
				}
				if icc != nil {
					t.Fatalf("%s: в варианте есть профиль (%d байт), ждали без профиля", o.Name, len(icc))
				}
				if vx := chunkData(cs, "VP8X"); vx != nil && vx[0]&0x20 != 0 {
					t.Fatalf("%s: флаг ICC в VP8X без профиля", o.Name)
				}
				noSubstring(t, o.Data, tc.profile)
			}
		})
	}
}

// Финальное ревью media, D-C1: разбор desc (mluc) ограничен — записей ≤ 64, запись ≤ 512 Б, всего ≤ 4 КиБ; больше —
// описание не читается и профиль не P3 (как любой битый профиль: варианты без профиля, картинка не отклоняется).
func TestICCDescLimits(t *testing.T) {
	t.Parallel()
	fill := func(n int, last string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "x"
		}
		out[n-1] = last
		return out
	}
	pad := func(s string, chars int) string { return s + strings.Repeat(" ", chars-len(s)) }
	big := pad("Display P3", 256) // 512 Б в UTF-16
	tests := []struct {
		name  string
		texts []string
		want  bool
	}{
		{"64 записи, P3 в последней", fill(64, "Display P3"), true},
		{"65 записей", fill(65, "Display P3"), false},
		{"запись 512 Б", []string{big}, true},
		{"запись 514 Б", []string{pad("Display P3", 257)}, false},
		{"8 записей по 512 Б — ровно 4 КиБ", slices.Repeat([]string{big}, 8), true},
		{"9 записей по 512 Б", slices.Repeat([]string{big}, 9), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isDisplayP3(p3WithDesc(tc.texts...)); got != tc.want {
				t.Fatalf("isDisplayP3 = %v, ждали %v", got, tc.want)
			}
		})
	}
}

// PNG 8×8 с iCCP в сотни байт (профиль 72 КБ: 6000 записей mluc «весь тег») и JPEG с тем же профилем в APP2 — до
// правки 7 ГиБ выделений и секунды CPU. Теперь профиль просто не P3: варианты без профиля, выделения — мелочь.
// serial: TotalAlloc — счётчик процесса, параллельные тесты исказили бы рост.
func TestICCDescBomb(t *testing.T) {
	prof := mlucBomb(6000)
	src := gradient(8, 8)
	for _, tc := range []struct {
		name string
		in   []byte
		max  int
	}{
		{"PNG iCCP", withPNGChunks(encPNG(t, src), iccpChunk(t, prof)), 1024},
		{"JPEG APP2", withJPEGSegs(encJPEG(t, src, 90), jpegICC(prof, 2, false)...), 80 << 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.in) > tc.max {
				t.Fatalf("вход %d байт, ждали ≤ %d", len(tc.in), tc.max)
			}
			var (
				outs []Output
				err  error
			)
			grew := allocDuring(func() {
				outs, _, err = Process(context.Background(), tc.in, Limits{MaxPixels: 1 << 20, Inputs: allInputs}, oneFull, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			if icc := chunkData(riffChunks(t, outs[0].Data), "ICCP"); icc != nil {
				t.Fatalf("в варианте профиль %d байт, ждали без профиля", len(icc))
			}
			if grew >= 16<<20 {
				t.Fatalf("за вызов выделено %d байт (≥ 16 МБ): разбор desc не ограничен", grew)
			}
		})
	}
}
