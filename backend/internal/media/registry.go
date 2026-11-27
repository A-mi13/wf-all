package media

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
)

// Registry — реестр целей (§2.2): собирается один раз при старте бинарника (mediapurposes, R12) и после NewRegistry
// не меняется — Get и Names отдают копии.
type Registry struct {
	byName map[string]Purpose
	names  []string
}

// slug — имя цели и варианта: имя варианта входит в ключ хранилища <object_id>/<вариант>.webp, поэтому без «/»,
// точек и заглавных.
var slug = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// NewRegistry — реестр из целей. Пустой список допустим. Ошибка (отказ старта) — дубль или негодное имя, видимость
// вне public | private, пустые или неизвестные входы, Lottie вместе с растром, MaxBytes ≤ 0, растр без MaxPixels,
// пустые варианты, имя варианта не слаг или дубль, MaxSide < 0, Quality вне 0–100 (у Lossy — вне 1–100: при 0
// кодировщик с потерями выдаёт мусор), пустые типы владельца.
func NewRegistry(ps ...Purpose) (*Registry, error) {
	r := &Registry{byName: make(map[string]Purpose, len(ps)), names: make([]string, 0, len(ps))}
	for _, p := range ps {
		if err := validate(p); err != nil {
			return nil, fmt.Errorf("media: цель %q: %w", p.Name, err)
		}
		if _, dup := r.byName[p.Name]; dup {
			return nil, fmt.Errorf("media: цель %q: дубль имени", p.Name)
		}
		r.byName[p.Name] = clonePurpose(p)
		r.names = append(r.names, p.Name)
	}
	sort.Strings(r.names)
	return r, nil
}

func validate(p Purpose) error {
	if !slug.MatchString(p.Name) {
		return errors.New("имя — латиница в нижнем регистре, цифры и _")
	}
	if p.Visibility != VisibilityPublic && p.Visibility != VisibilityPrivate {
		return fmt.Errorf("видимость %q — не public и не private", p.Visibility)
	}
	if len(p.Inputs) == 0 {
		return errors.New("пустые входы")
	}
	lottie := false
	for _, in := range p.Inputs {
		switch in {
		case InputJPEG, InputPNG, InputWebP:
		case InputLottie:
			lottie = true
		default:
			return fmt.Errorf("неизвестный вход %q", in)
		}
	}
	if lottie && len(p.Inputs) > 1 {
		return errors.New("растровые входы вместе с Lottie")
	}
	if p.MaxBytes <= 0 {
		return errors.New("MaxBytes ≤ 0")
	}
	if !lottie && p.MaxPixels <= 0 {
		return errors.New("растровая цель без MaxPixels")
	}
	if len(p.Variants) == 0 {
		return errors.New("пустые варианты")
	}
	seen := make(map[string]bool, len(p.Variants))
	for _, v := range p.Variants {
		switch {
		case !slug.MatchString(v.Name):
			return fmt.Errorf("вариант %q: имя — латиница в нижнем регистре, цифры и _", v.Name)
		case seen[v.Name]:
			return fmt.Errorf("вариант %q: дубль", v.Name)
		case v.MaxSide < 0:
			return fmt.Errorf("вариант %q: MaxSide < 0", v.Name)
		case v.Quality < 0 || v.Quality > 100:
			return fmt.Errorf("вариант %q: Quality вне 0–100", v.Name)
		case v.Lossy && v.Quality == 0:
			return fmt.Errorf("вариант %q: Lossy с Quality 0 — нужен 1–100", v.Name)
		}
		seen[v.Name] = true
	}
	if len(p.OwnerTypes) == 0 || slices.Contains(p.OwnerTypes, "") {
		return errors.New("пустые типы владельца")
	}
	return nil
}

func clonePurpose(p Purpose) Purpose {
	p.Inputs = slices.Clone(p.Inputs)
	p.Variants = slices.Clone(p.Variants)
	p.OwnerTypes = slices.Clone(p.OwnerTypes)
	return p
}

// Get — цель по имени (копия).
func (r *Registry) Get(name string) (Purpose, bool) {
	p, ok := r.byName[name]
	if !ok {
		return Purpose{}, false
	}
	return clonePurpose(p), true
}

// Names — имена целей по алфавиту (копия): страж одинаковости реестра у трёх бинарников сравнивает их.
func (r *Registry) Names() []string { return slices.Clone(r.names) }
