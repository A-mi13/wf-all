package domain

import (
	"testing"
	"unicode"
)

// Таблица pictographic вписана руками (в stdlib свойства нет): unicode.Is ищет двоичным поиском — диапазоны
// по возрастанию без пересечений, LatinOffset — число диапазонов R16 в Latin-1. Точки — по краям диапазонов
// и рядом с ними (модификаторы тона и региональные индикаторы — не пиктограммы).
func TestPictographicTable(t *testing.T) {
	var prev uint32
	latin := 0
	for i, r := range pictographic.R16 {
		if r.Lo > r.Hi || r.Stride != 1 || i > 0 && uint32(r.Lo) <= prev+1 {
			t.Fatalf("R16[%d] = %+v после %#x", i, r, prev)
		}
		if r.Hi <= unicode.MaxLatin1 {
			latin++
		}
		prev = uint32(r.Hi)
	}
	for i, r := range pictographic.R32 {
		if r.Lo > r.Hi || r.Stride != 1 || r.Lo <= prev+1 {
			t.Fatalf("R32[%d] = %+v после %#x", i, r, prev)
		}
		prev = r.Hi
	}
	if latin != pictographic.LatinOffset {
		t.Fatalf("LatinOffset = %d, диапазонов Latin-1 %d", pictographic.LatinOffset, latin)
	}
	for r, want := range map[rune]bool{
		0x00A9: true, 0x00AE: true, 0x2764: true, 0x26BD: true, 0x1F468: true, 0x1F4BB: true, 0x1F3F3: true,
		0x1F308: true, 0x1F9B0: true, 0x1FAFF: true, 0x1F3FA: true, 0x1F400: true,
		'A': false, '1': false, 0x00A8: false, 0x2765 + 3: false, 0x1F3FB: false, 0x1F3FF: false, 0x1F1E6: false,
		0x1F1FF: false, 0x200D: false, 0xFE0F: false,
	} {
		if got := unicode.Is(pictographic, r); got != want {
			t.Errorf("pictographic(%#x) = %v, ждали %v", r, got, want)
		}
	}
}
