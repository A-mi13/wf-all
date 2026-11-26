package media_test

import (
	"context"
	"image"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/media"
)

// photo — допустимая растровая цель; правки табличного теста меняют по одному полю.
func photo(name string) media.Purpose {
	return media.Purpose{
		Name: name, Visibility: media.VisibilityPublic,
		Inputs:   []media.InputKind{media.InputJPEG, media.InputPNG, media.InputWebP},
		MaxBytes: 15 << 20, MaxPixels: 40_000_000,
		Variants: []media.Variant{
			{Name: "thumb", MaxSide: 320, Lossy: true, Quality: 80},
			{Name: "full", MaxSide: 1920, Lossy: true, Quality: 80},
		},
		OwnerTypes: []string{"pitch_photo"},
	}
}

// animation — допустимая цель Lottie: без предела пикселей, вариант «как есть».
func animation() media.Purpose {
	return media.Purpose{Name: "catalog_animation", Visibility: media.VisibilityPublic,
		Inputs: []media.InputKind{media.InputLottie}, MaxBytes: 1 << 20,
		Variants: []media.Variant{{Name: "anim"}}, OwnerTypes: []string{"catalog_item"}}
}

func edit(p media.Purpose, f func(p *media.Purpose)) media.Purpose {
	f(&p)
	return p
}

// Реестр проверяется при сборке бинарника: ошибка цели — отказ старта, а не 500 или «бомба» в рантайме. Пустой
// список допустим (прод этого плана — без целей, R12).
func TestRegistryValidation(t *testing.T) {
	t.Parallel()
	if r, err := media.NewRegistry(); err != nil || len(r.Names()) != 0 {
		t.Fatalf("пустой реестр: %v, %v", r, err)
	}
	if _, err := media.NewRegistry(photo("pitch_photo"), animation(), edit(photo("dispute_evidence"), func(p *media.Purpose) {
		p.Visibility = media.VisibilityPrivate
	})); err != nil {
		t.Fatalf("допустимые цели: %v", err)
	}
	for _, c := range []struct {
		name string
		ps   []media.Purpose
		want string // часть текста ошибки
	}{
		{"дубль имени", []media.Purpose{photo("pitch_photo"), photo("pitch_photo")}, "дубль"},
		{"пустое имя", []media.Purpose{photo("")}, "имя"},
		{"имя не слаг", []media.Purpose{photo("Pitch Photo")}, "имя"},
		{"видимость", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.Visibility = "secret" })}, "видимость"},
		{"пустые входы", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.Inputs = nil })}, "входы"},
		{"неизвестный вход", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.Inputs = []media.InputKind{"heic"} })}, "вход"},
		{"Lottie с растром", []media.Purpose{edit(animation(), func(p *media.Purpose) {
			p.Inputs = append(p.Inputs, media.InputPNG)
		})}, "Lottie"},
		{"MaxBytes 0", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.MaxBytes = 0 })}, "MaxBytes"},
		{"MaxBytes < 0", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.MaxBytes = -1 })}, "MaxBytes"},
		{"растр без MaxPixels", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.MaxPixels = 0 })}, "MaxPixels"},
		{"пустые варианты", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.Variants = nil })}, "варианты"},
		{"имя варианта с /", []media.Purpose{edit(photo("p"), func(p *media.Purpose) {
			p.Variants = []media.Variant{{Name: "../full", MaxSide: 10}}
		})}, "вариант"},
		{"пустое имя варианта", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.Variants = []media.Variant{{MaxSide: 10}} })}, "вариант"},
		{"дубль варианта", []media.Purpose{edit(photo("p"), func(p *media.Purpose) {
			p.Variants = []media.Variant{{Name: "full", MaxSide: 10}, {Name: "full", MaxSide: 20}}
		})}, "вариант"},
		{"MaxSide < 0", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.Variants = []media.Variant{{Name: "full", MaxSide: -1}} })}, "MaxSide"},
		{"Quality > 100", []media.Purpose{edit(photo("p"), func(p *media.Purpose) {
			p.Variants = []media.Variant{{Name: "full", Lossy: true, Quality: 101}}
		})}, "Quality"},
		{"без типов владельца", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.OwnerTypes = nil })}, "владельц"},
		{"пустой тип владельца", []media.Purpose{edit(photo("p"), func(p *media.Purpose) { p.OwnerTypes = []string{""} })}, "владельц"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r, err := media.NewRegistry(c.ps...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ждали ошибку с %q, получили %v (%v)", c.want, err, r)
			}
		})
	}
}

// Names — по алфавиту (страж одинаковости реестра у трёх бинарников сравнивает списки), копией.
func TestRegistryNamesSorted(t *testing.T) {
	t.Parallel()
	r, err := media.NewRegistry(photo("pitch_photo"), animation(), photo("dispute_evidence"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"catalog_animation", "dispute_evidence", "pitch_photo"}
	if got := r.Names(); !slices.Equal(got, want) {
		t.Fatalf("Names = %v", got)
	}
	r.Names()[0] = "x"
	if got := r.Names(); !slices.Equal(got, want) {
		t.Fatalf("Names после правки копии = %v", got)
	}
}

// Реестр неизменяем после New: ни правка среза вызывающего, ни правка цели из Get его не меняют.
func TestRegistryImmutable(t *testing.T) {
	t.Parallel()
	p := photo("pitch_photo")
	r, err := media.NewRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Inputs[0], p.Variants[0].MaxSide, p.OwnerTypes[0] = media.InputLottie, 99999, "x"
	got, ok := r.Get("pitch_photo")
	if !ok {
		t.Fatal("цели нет")
	}
	got.Inputs[0], got.Variants[0].MaxSide, got.OwnerTypes[0] = media.InputLottie, 99999, "x"
	again, _ := r.Get("pitch_photo")
	want := photo("pitch_photo")
	if !slices.Equal(again.Inputs, want.Inputs) || !slices.Equal(again.Variants, want.Variants) ||
		!slices.Equal(again.OwnerTypes, want.OwnerTypes) {
		t.Fatalf("реестр изменён снаружи: %+v", again)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("неизвестная цель найдена")
	}
}

// CheckOff (§8: проверка содержимого вручную) — всё allow без ошибки.
func TestCheckOffAllows(t *testing.T) {
	t.Parallel()
	v, err := media.CheckOff.Check(context.Background(), image.NewRGBA(image.Rect(0, 0, 13, 13)))
	if err != nil || !v.Allow || v.Category != "" {
		t.Fatalf("CheckOff: %+v, %v", v, err)
	}
}
