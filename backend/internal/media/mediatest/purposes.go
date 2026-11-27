// Package mediatest — тестовые помощники media: цели e2e (R12: их подключает cmd/internal/mediapurposes под тегом
// wf_e2e), тестовая проверка содержимого (R20) и сборка сервиса на хранилище в памяти. Импортируют только тесты и файлы
// cmd/* с тегом wf_e2e.
package mediatest

import "wf/backend/internal/media"

// Тестовые цели и тип владельца (e2e и тесты модуля).
const (
	PurposeImage        = "e2e_image"
	PurposeImagePrivate = "e2e_image_private"
	OwnerType           = "e2e_owner"
	// MaxBytes — предел обеих целей.
	MaxBytes = 10 << 20
)

func imagePurpose(name string, v media.Visibility) media.Purpose {
	return media.Purpose{
		Name:       name,
		Visibility: v,
		Inputs:     []media.InputKind{media.InputJPEG, media.InputPNG, media.InputWebP},
		MaxBytes:   MaxBytes,
		MaxPixels:  40_000_000,
		Variants: []media.Variant{
			{Name: "thumb", MaxSide: 256, Lossy: true, Quality: 80},
			{Name: "full", MaxSide: 2048, Lossy: true, Quality: 85},
		},
		OwnerTypes:   []string{OwnerType},
		ContentCheck: true,
	}
}

// E2EPurposes — две тестовые цели: публичная и закрытая картинка владельца e2e_owner.
func E2EPurposes() []media.Purpose {
	return []media.Purpose{
		imagePurpose(PurposeImage, media.VisibilityPublic),
		imagePurpose(PurposeImagePrivate, media.VisibilityPrivate),
	}
}
