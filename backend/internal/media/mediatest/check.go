package mediatest

import (
	"context"
	"image"

	"wf/backend/internal/media"
)

// MarkerCategory — категория отказа MarkerCheck.
const MarkerCategory = "test_marker"

// MarkerSide — сторона картинки-метки: ровно MarkerSide × MarkerSide пикселей отклоняется.
const MarkerSide = 13

// MarkerCheck — тестовая проверка содержимого (R20, спека §8): картинка ровно 13×13 — reject(test_marker), прочие —
// allow. Прод — media.CheckOff.
var MarkerCheck media.ContentCheck = markerCheck{}

type markerCheck struct{}

func (markerCheck) Check(_ context.Context, img image.Image) (media.Verdict, error) {
	if b := img.Bounds(); b.Dx() == MarkerSide && b.Dy() == MarkerSide {
		return media.Verdict{Category: MarkerCategory}, nil
	}
	return media.Verdict{Allow: true}, nil
}
