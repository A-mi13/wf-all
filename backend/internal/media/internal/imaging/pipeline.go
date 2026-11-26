package imaging

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"slices"

	"github.com/deepteams/webp"
	xwebp "golang.org/x/image/webp"
)

// maxSourceSide — предел длинной стороны входа (по заголовку, до декодирования): веса и буфер ресайза растут с ней.
const maxSourceSide = 65535

// ErrContentRejected — проверка содержимого отвергла картинку; категория — в *ContentRejectedError.
var ErrContentRejected = errors.New("imaging: content rejected")

// ContentRejectedError — отказ проверки содержимого с категорией; errors.Is(err, ErrContentRejected) — истина.
type ContentRejectedError struct{ Category string }

func (e *ContentRejectedError) Error() string { return ErrContentRejected.Error() + ": " + e.Category }

func (e *ContentRejectedError) Is(target error) bool { return target == ErrContentRejected }

// Limits — пределы цели: пиксели (защита от «бомбы»; 0 — отказ любой картинке) и допустимые входы.
type Limits struct {
	MaxPixels int64
	Inputs    []Kind
}

// Spec — вариант: имя (часть ключа хранилища), предел длинной стороны (≤ 0 — без масштаба), сжатие WebP
// (Lossy=false — без потерь; Quality 0–100).
type Spec struct {
	Name    string
	MaxSide int
	Lossy   bool
	Quality int
}

// Output — готовый вариант WebP.
type Output struct {
	Name, ContentType string
	Width, Height     int
	Data              []byte
}

// Process — шаги 3–6 §3.3: заголовок, лимиты, декодирование, ориентация, проверка содержимого, варианты.
//
// Отказы (объект отклоняется): ErrUnsupported (тип не из lim.Inputs, анимация, неподдерживаемый вариант формата),
// ErrTooManyPixels, ErrCorrupt, ErrContentRejected (*ContentRejectedError с категорией). Прочие ошибки — отмена ctx,
// сбой check, сбой кодировщика — не отказ: задача повторяется. Header — размеры после EXIF-ориентации.
//
// Память: пиксели проверяются по заголовку до декодирования — декодер выделяет память по тем же размерам
// (DecodeConfig того же пакета), так что пик ограничен MaxPixels × байт на пиксель (до 8 у PNG 16 бит) плюс
// буфер уменьшения; варианты уменьшаются каскадом (каждый — из наименьшего готового не меньше себя), ориентация
// применяется к уменьшенным.
func Process(ctx context.Context, b []byte, lim Limits, variants []Spec,
	check func(image.Image) (bool, string, error),
) ([]Output, Header, error) {
	if err := ctx.Err(); err != nil {
		return nil, Header{}, err
	}
	in, err := inspect(b)
	if err != nil {
		return nil, Header{}, err
	}
	h := in.Header
	if !slices.Contains(lim.Inputs, h.Kind) {
		return nil, h, fmt.Errorf("%w: %s не из входов цели", ErrUnsupported, h.Kind)
	}
	if h.Animated {
		return nil, h, fmt.Errorf("%w: анимация", ErrUnsupported)
	}
	if int64(in.rawW)*int64(in.rawH) > lim.MaxPixels {
		return nil, h, fmt.Errorf("%w: %d×%d", ErrTooManyPixels, in.rawW, in.rawH)
	}
	// Полоса 1×L проходит предел пикселей, но ресайз тратит память пропорционально длинной стороне.
	if max(in.rawW, in.rawH) > maxSourceSide {
		return nil, h, fmt.Errorf("%w: сторона %d > %d", ErrTooManyPixels, max(in.rawW, in.rawH), maxSourceSide)
	}
	// Вариант без масштаба больше предела WebP — отказ здесь, а не ошибка кодировщика с повторами задачи.
	for _, s := range variants {
		if w, ht := targetSize(in.rawW, in.rawH, s.MaxSide); max(w, ht) > webp.MaxDimension {
			return nil, h, fmt.Errorf("%w: вариант %q %d×%d больше предела WebP", ErrTooManyPixels, s.Name, w, ht)
		}
	}
	profile, err := outputProfile(in)
	if err != nil {
		return nil, h, err
	}
	img, err := decode(h.Kind, b)
	if err != nil {
		return nil, h, err
	}
	if err := ctx.Err(); err != nil {
		return nil, h, err
	}
	if check != nil {
		ok, category, err := check(oriented(img, in.orientation))
		if err != nil {
			return nil, h, fmt.Errorf("imaging: проверка содержимого: %w", err)
		}
		if !ok {
			return nil, h, &ContentRejectedError{Category: category}
		}
	}
	outs, err := render(ctx, img, in, variants, profile)
	if err != nil {
		return nil, h, err
	}
	return outs, h, nil
}

// outputProfile — профиль для вариантов: эталон P3, если профиль входа распознан как Display P3, иначе нет.
// Байты профиля пользователя в выход не идут никогда (R2-I2).
func outputProfile(in info) ([]byte, error) {
	icc := in.icc
	if in.iccp != nil {
		var err error
		if icc, err = inflateICCP(in.iccp); err != nil {
			return nil, err
		}
	}
	if isDisplayP3(icc) {
		return referenceP3, nil
	}
	return nil, nil
}

func decode(k Kind, b []byte) (image.Image, error) {
	r := bytes.NewReader(b)
	var (
		m   image.Image
		err error
	)
	switch k {
	case KindJPEG:
		m, err = jpeg.Decode(r)
	case KindPNG:
		m, err = png.Decode(r)
	case KindWebP:
		m, err = xwebp.Decode(r)
	default:
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, decodeError(err)
	}
	return m, nil
}

// render — варианты в порядке запроса; уменьшение каскадом от большего к меньшему.
func render(ctx context.Context, img image.Image, in info, variants []Spec, profile []byte) ([]Output, error) {
	type job struct {
		i    int
		w, h int // до ориентации
	}
	jobs := make([]job, len(variants))
	for i, s := range variants {
		w, h := targetSize(in.rawW, in.rawH, s.MaxSide)
		jobs[i] = job{i, w, h}
	}
	slices.SortStableFunc(jobs, func(a, b job) int { return cmp.Compare(b.w*b.h, a.w*a.h) })

	outs := make([]Output, len(variants))
	var made []*image.NRGBA // готовые (до ориентации), по убыванию
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src := img
		for _, m := range made {
			if m.Rect.Dx() >= j.w && m.Rect.Dy() >= j.h {
				src = m // последний подходящий — наименьший
			}
		}
		small := resize(src, j.w, j.h)
		made = append(made, small)
		v := orientNRGBA(small, in.orientation)
		data, err := encodeWebP(v, variants[j.i], profile)
		if err != nil {
			return nil, err
		}
		outs[j.i] = Output{
			Name: variants[j.i].Name, ContentType: contentTypeWebP,
			Width: v.Rect.Dx(), Height: v.Rect.Dy(), Data: data,
		}
	}
	return outs, nil
}
