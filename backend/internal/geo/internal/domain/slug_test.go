package domain_test

import (
	"slices"
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

func TestSlugCandidates(t *testing.T) {
	cases := []struct {
		base, region string
		n            int
		want         []string
	}{
		{"mikhaylovsk", "Stavropol Kray", 4, []string{"mikhaylovsk", "mikhaylovsk-stavropol-kray", "mikhaylovsk-2", "mikhaylovsk-3"}},
		{"mikhaylovsk", "Sverdlovsk Oblast", 2, []string{"mikhaylovsk", "mikhaylovsk-sverdlovsk-oblast"}},
		{"zarechnyy", "", 3, []string{"zarechnyy", "zarechnyy-2", "zarechnyy-3"}},
		{"zarechnyy", "Пензенская", 2, []string{"zarechnyy", "zarechnyy-2"}},
		{"stavropol", "Stavropol Kray", 1, []string{"stavropol"}},
		{"stavropol", "Stavropol Kray", 0, nil},
		{"", "Stavropol Kray", 3, nil},
	}
	for _, c := range cases {
		if got := domain.SlugCandidates(c.base, c.region, c.n); !slices.Equal(got, c.want) {
			t.Errorf("SlugCandidates(%q, %q, %d) = %q, ждали %q", c.base, c.region, c.n, got, c.want)
		}
	}
}
