package mediatest_test

import (
	"context"
	"image"
	"testing"

	"wf/backend/internal/media/mediatest"
)

// R20: ровно 13×13 — reject(test_marker); соседние размеры — allow.
func TestMarkerCheck(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		w, h  int
		allow bool
	}{{13, 13, false}, {13, 14, true}, {12, 13, true}, {100, 100, true}} {
		v, err := mediatest.MarkerCheck.Check(context.Background(), image.NewRGBA(image.Rect(0, 0, c.w, c.h)))
		if err != nil || v.Allow != c.allow || (!c.allow && v.Category != mediatest.MarkerCategory) {
			t.Errorf("%d×%d: %+v %v", c.w, c.h, v, err)
		}
	}
}

// Цели e2e проходят реестр и различаются видимостью.
func TestE2EPurposesRegistry(t *testing.T) {
	t.Parallel()
	reg, err := mediatest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	pub, ok1 := reg.Get(mediatest.PurposeImage)
	priv, ok2 := reg.Get(mediatest.PurposeImagePrivate)
	if !ok1 || !ok2 || pub.Visibility != "public" || priv.Visibility != "private" {
		t.Fatalf("цели: %+v %+v", pub, priv)
	}
}
