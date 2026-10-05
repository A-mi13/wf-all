package source

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCapReader(t *testing.T) {
	r := &capReader{rc: io.NopCloser(strings.NewReader(strings.Repeat("x", 11))), limit: 10}
	if _, err := io.ReadAll(r); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("11 байт при лимите 10: %v", err)
	}
	r = &capReader{rc: io.NopCloser(strings.NewReader(strings.Repeat("x", 10))), limit: 10}
	if b, err := io.ReadAll(r); err != nil || len(b) != 10 {
		t.Fatalf("ровно 10: %v %d", err, len(b))
	}
}
