//go:build !wf_e2e

package mediapurposes

import (
	"testing"

	"wf/backend/internal/media"
)

// R12: прод-сборка (без тега wf_e2e) — ни одной тестовой цели (в этом плане целей прода нет), проверка содержимого
// выключена; реестр собирается.
func TestProdPurposes(t *testing.T) {
	t.Parallel()
	if p := Purposes(); len(p) != 0 {
		t.Fatalf("прод-сборка: цели %v", p)
	}
	r, err := Registry()
	if err != nil || len(r.Names()) != 0 {
		t.Fatalf("реестр прод-сборки: %v %v", r, err)
	}
	if Check() != media.CheckOff {
		t.Fatal("прод-сборка: проверка содержимого не CheckOff")
	}
}
