//go:build wf_e2e

package mediapurposes

import (
	"wf/backend/internal/media"
	"wf/backend/internal/media/mediatest"
)

// Purposes — цели сборки живого e2e (тег wf_e2e): список прода и тестовые e2e_image, e2e_image_private. Прод-образ
// (backend/Dockerfile) собирается без тега.
func Purposes() []media.Purpose { return append(prodPurposes(), mediatest.E2EPurposes()...) }

// Check — тестовая проверка содержимого e2e (R20): картинка 13×13 — reject(test_marker).
func Check() media.ContentCheck { return mediatest.MarkerCheck }
