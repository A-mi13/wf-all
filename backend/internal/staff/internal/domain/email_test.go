package domain_test

import (
	"strings"
	"testing"

	"wf/backend/internal/staff/internal/domain"
)

// Review Focus 1: один адрес в любом написании — одна строка.
func TestNormalizeEmail(t *testing.T) {
	const want = "maria.k@example.com"
	same := []string{
		"maria.k@example.com",
		"  Maria.K@Example.COM ",
		"MARIA.K@EXAMPLE.COM",
		"Ｍａｒｉａ.Ｋ＠Ｅｘａｍｐｌｅ.ＣＯＭ",           // полноширинные буквы и '＠' (NFKC)
		"\u3000maria.k@example.com\t\n", // идеографический пробел и табуляция по краям
	}
	for _, in := range same {
		if got, ok := domain.NormalizeEmail(in); !ok || got != want {
			t.Errorf("NormalizeEmail(%q) = %q %v, ждали %q true", in, got, ok, want)
		}
	}
	local := strings.Repeat("a", 254-len("@example.com"))
	if got, ok := domain.NormalizeEmail(local + "@example.com"); !ok || len(got) != 254 {
		t.Errorf("254 байта: %v", ok)
	}
	bad := []string{
		"",
		"   ",
		"maria.k",
		"@example.com",
		"maria.k@",
		"maria@k@example.com",
		"maria k@example.com",
		"maria.k@exa mple.com",
		"\u200bmaria.k@example.com", // пробел нулевой ширины (Cf) — двойник учётки
		"maria.k@exa\u202emple.com", // смена направления письма (Cf)
		"maria.k@example.com\ufeff", // BOM в хвосте (Cf)
		"maria\u0000k@example.com",
		"a" + local + "@example.com", // 255 байт
	}
	for _, in := range bad {
		if got, ok := domain.NormalizeEmail(in); ok {
			t.Errorf("NormalizeEmail(%q) = %q, ждали отказ", in, got)
		}
	}
}
