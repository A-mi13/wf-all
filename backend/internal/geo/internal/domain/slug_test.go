package domain_test

import (
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/geo/internal/domain"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Stavropol":           "stavropol",
		"Mikhaylovsk":         "mikhaylovsk",
		"Stavropol'":          "stavropol",
		"  Nizhniy Novgorod ": "nizhniy-novgorod",
		"Rostov-na-Donu":      "rostov-na-donu",
		"Sankt--Peterburg__2": "sankt-peterburg-2",
		"Ust’-Labinsk":        "ust-labinsk",
		"--A--":               "a",
		"Ставрополь":          "",
		"":                    "",
	}
	for in, want := range cases {
		if got := domain.Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestActivationSlug(t *testing.T) {
	long := strings.Repeat("abcdefghi-", 15) // 150 символов, части по 9 букв
	cases := []struct {
		name  string
		ascii string
		id    int64
		want  string
	}{
		{"обычное", "Stavropol", 487846, "stavropol"},
		{"обычное 2", "Mikhaylovsk", 526815, "mikhaylovsk"},
		{"апостроф в начале", "'Ayn Dar", 1, "ayn-dar"},
		{"Slugify пуст — city-<id>", "ʻ", 123, "city-123"},
		{"пустая строка — city-<id>", "", 7, "city-7"},
		// 150 символов: обрезка по границе части (последний дефис до 100), без хвостового дефиса
		{"длинное — по границе части", long, 1, strings.TrimSuffix(strings.Repeat("abcdefghi-", 10), "-")},
		// одна часть длиннее 100 — границы нет, жёсткая обрезка
		{"одна длинная часть", strings.Repeat("a", 150), 1, strings.Repeat("a", 100)},
		// ровно 100 — без изменений
		{"ровно 100", strings.Repeat("b", 100), 1, strings.Repeat("b", 100)},
	}
	for _, c := range cases {
		got := domain.ActivationSlug(c.ascii, c.id)
		if got != c.want {
			t.Errorf("%s: ActivationSlug(%q, %d) = %q, ждали %q", c.name, c.ascii, c.id, got, c.want)
		}
		if !domain.ValidSlug(got) {
			t.Errorf("%s: ActivationSlug(%q) = %q — не проходит ValidSlug", c.name, c.ascii, got)
		}
	}
}

// first — первые n кандидатов бесконечной последовательности.
func first(seq func(func(string) bool), n int) []string {
	var out []string
	for s := range seq {
		if len(out) == n {
			break
		}
		out = append(out, s)
	}
	return out
}

func TestSlugCandidates(t *testing.T) {
	cases := []struct {
		name, base, region string
		n                  int
		want               []string
	}{
		{"занят — регион — номера", "mikhaylovsk", "Sverdlovsk Oblast", 4,
			[]string{"mikhaylovsk", "mikhaylovsk-sverdlovsk-oblast", "mikhaylovsk-2", "mikhaylovsk-3"}},
		{"регион без ascii — сразу -2", "zarechnyy", "", 3, []string{"zarechnyy", "zarechnyy-2", "zarechnyy-3"}},
		{"регион из одних нелатинских — сразу -2", "zarechnyy", "Пензенская", 2, []string{"zarechnyy", "zarechnyy-2"}},
		{"пустая база — ничего", "", "Stavropol Kray", 3, nil},
	}
	for _, c := range cases {
		if got := first(domain.SlugCandidates(c.base, c.region), c.n); !slices.Equal(got, c.want) {
			t.Errorf("%s: SlugCandidates(%q, %q) = %q, ждали %q", c.name, c.base, c.region, got, c.want)
		}
	}

	t.Run("база 99 + -2 — база обрезана до 98 (98 + 2 = SlugMax)", func(t *testing.T) {
		base := strings.Repeat("a", 99)
		got := first(domain.SlugCandidates(base, ""), 2)
		want := []string{base, strings.Repeat("a", 98) + "-2"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %q, ждали %q", got, want)
		}
	})

	t.Run("база 100 + регион — база обрезана по границе части, без -- и хвостового -", func(t *testing.T) {
		base := strings.Repeat("abcdefghi-", 9) + "abcdefghi" // 99 символов, 10 частей
		got := first(domain.SlugCandidates(base, "Sverdlovsk Oblast"), 3)
		if len(got) != 3 {
			t.Fatalf("кандидатов %d, ждали 3: %q", len(got), got)
		}
		for _, s := range got {
			if !domain.ValidSlug(s) {
				t.Errorf("кандидат %q не проходит ValidSlug", s)
			}
		}
		// под суффикс -sverdlovsk-oblast (18) базе остаётся 82: восьмая часть разорвана — режем до седьмой
		if want := strings.Repeat("abcdefghi-", 8) + "sverdlovsk-oblast"; got[1] != want {
			t.Errorf("кандидат с регионом = %q, ждали %q", got[1], want)
		}
	})

	t.Run("номер растёт — база режется сильнее", func(t *testing.T) {
		base := strings.Repeat("a", 100)
		var got []string
		for s := range domain.SlugCandidates(base, "") {
			got = append(got, s)
			if len(got) == 11 { // base, -2 … -11
				break
			}
		}
		if len(got) != 11 {
			t.Fatalf("кандидатов %d, ждали 11", len(got))
		}
		if got[10] != strings.Repeat("a", 97)+"-11" {
			t.Fatalf("11-й кандидат = %q, ждали a×97-11", got[10])
		}
		if !slices.Equal(got[:2], []string{base, strings.Repeat("a", 98) + "-2"}) {
			t.Fatalf("начало = %q", got[:2])
		}
	})

	t.Run("регион длиннее лимита — кандидат пропущен, дальше номера", func(t *testing.T) {
		got := first(domain.SlugCandidates("abc", strings.Repeat("r", 120)), 2)
		if !slices.Equal(got, []string{"abc", "abc-2"}) {
			t.Fatalf("got %q", got)
		}
	})
}

func TestValidSlug(t *testing.T) {
	cases := map[string]bool{
		"stavropol":                     true,
		"mikhaylovsk-sverdlovsk-oblast": true,
		"city-123":                      true,
		"a":                             true,
		strings.Repeat("a", 100):        true,
		strings.Repeat("a", 101):        false,
		"":                              false,
		"a--b":                          false,
		"-a":                            false,
		"a-":                            false,
		"Stavropol":                     false,
		"stavropol_2":                   false,
		"ставрополь":                    false,
		"a b":                           false,
	}
	for in, want := range cases {
		if got := domain.ValidSlug(in); got != want {
			t.Errorf("ValidSlug(%q) = %v, ждали %v", in, got, want)
		}
	}
}
