//go:build !wf_e2e

package mediapurposes

import "wf/backend/internal/media"

// Purposes — цели прод-сборки (без тега wf_e2e): только общий список прода.
func Purposes() []media.Purpose { return prodPurposes() }

// Check — проверка содержимого прод-сборки: выключена (спека §8, R20).
func Check() media.ContentCheck { return media.CheckOff }
