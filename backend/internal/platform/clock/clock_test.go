package clock_test

import (
	"errors"
	"testing"
	"time"

	"wf/backend/internal/platform/clock"
)

func TestSystemIsUTCNow(t *testing.T) {
	before := time.Now()
	got := clock.System.Now()
	if got.Location() != time.UTC {
		t.Fatalf("локация = %v, нужен UTC", got.Location())
	}
	if got.Before(before.Add(-time.Second)) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("время %v далеко от текущего", got)
	}
}

func TestInPicksFirstNonEmptyZone(t *testing.T) {
	at := time.Date(2026, 10, 1, 17, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		zones  []string
		offset int // секунды от UTC
	}{
		{"поле задано", []string{"Europe/Moscow", "Asia/Novosibirsk"}, 3 * 3600},
		{"поле пусто — город", []string{"", "Asia/Novosibirsk"}, 7 * 3600},
		// получасовой пояс: ночной пересчёт §9.3 не должен на нём промахиваться
		{"получасовой сдвиг", []string{"Asia/Kolkata"}, 5*3600 + 1800},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := clock.In(at, c.zones...)
			if err != nil {
				t.Fatal(err)
			}
			if _, off := got.Zone(); off != c.offset {
				t.Fatalf("сдвиг = %d, нужен %d", off, c.offset)
			}
			if !got.Equal(at) {
				t.Fatal("момент времени изменился — In должна менять только представление")
			}
		})
	}
}

func TestInErrors(t *testing.T) {
	at := time.Now()
	if _, err := clock.In(at); !errors.Is(err, clock.ErrNoZone) {
		t.Fatalf("без таймзон: %v, нужна ErrNoZone", err)
	}
	if _, err := clock.In(at, "", ""); !errors.Is(err, clock.ErrNoZone) {
		t.Fatalf("только пустые: %v, нужна ErrNoZone", err)
	}
	// неизвестная таймзона — ошибка, а не тихий UTC
	if _, err := clock.In(at, "Mars/Olympus"); err == nil {
		t.Fatal("неизвестная таймзона принята")
	}
	// Local — таймзона машины, а не места: в данных её быть не может
	if _, err := clock.In(at, "Local"); err == nil {
		t.Fatal("Local принята")
	}
}
