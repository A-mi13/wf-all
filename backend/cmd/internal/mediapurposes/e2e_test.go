//go:build wf_e2e

package mediapurposes

import (
	"slices"
	"testing"

	"wf/backend/internal/media/mediatest"
)

// R12, R20: сборка живого e2e — ровно две тестовые цели и тестовая проверка содержимого.
func TestE2EPurposes(t *testing.T) {
	t.Parallel()
	r, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.Names(), []string{mediatest.PurposeImage, mediatest.PurposeImagePrivate}; !slices.Equal(got, want) {
		t.Fatalf("цели e2e: %v, ждали %v", got, want)
	}
	if Check() != mediatest.MarkerCheck {
		t.Fatal("e2e: проверка содержимого не MarkerCheck")
	}
}
