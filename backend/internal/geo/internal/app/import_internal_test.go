package app

import (
	"testing"
	"time"
)

// Поле to альтернативного названия: год, дата трёх форматов (все встречаются в RU, 02.10.2026);
// непонятное значение — истёкшее (лучше не взять название, чем взять устаревшее).
func TestEnded(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	cases := map[string]bool{
		"": false, "1946": true, "2025": true, "2026": false, "2030": false,
		"28.07.1997": true, "01.10.2026": true, "02.10.2026": false,
		"2015-03-01": true, "2099-01-01": false, "19970728": true, "20991231": false,
		"весной": true,
	}
	for to, want := range cases {
		if got := ended(to, now); got != want {
			t.Errorf("ended(%q) = %v, want %v", to, got, want)
		}
	}
}
