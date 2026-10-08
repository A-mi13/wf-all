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
	// локальная часть — до 64 байт (RFC 5321 §4.5.3.1.1), считается по хранимой ASCII-форме
	local := strings.Repeat("a", 64)
	if got, ok := domain.NormalizeEmail(local + "@example.com"); !ok || got != local+"@example.com" {
		t.Errorf("локальная часть 64 байта: %q %v", got, ok)
	}
	if got, ok := domain.NormalizeEmail(strings.Repeat("ａ", 64) + "@example.com"); !ok || got != local+"@example.com" {
		t.Errorf("64 полноширинные буквы (192 байта ввода, 64 после NFKC): %q %v", got, ok)
	}
	// общий предел 254 — длинным доменом (метки до 63, домен до 253 байт — их держит IDNA)
	host := strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	if got, ok := domain.NormalizeEmail(local + "@" + host); !ok || len(got) != 254 {
		t.Errorf("254 байта: %d %v", len(got), ok)
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
		"a" + local + "@example.com", // локальная часть 65 байт
		local + "@" + host + "d",     // 255 байт: метка 62 и домен 190 — в пределах IDNA, сверх общего предела
	}
	for _, in := range bad {
		if got, ok := domain.NormalizeEmail(in); ok {
			t.Errorf("NormalizeEmail(%q) = %q, ждали отказ", in, got)
		}
	}
}

// Домен IDN хранится A-label (UTS #46, профиль регистрации), локальная часть — только печатный
// ASCII (без SMTPUTF8, RFC 6531). Ожидания A-label сверены независимо: node url.domainToASCII.
func TestNormalizeEmailIDN(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"кириллический домен", "user@\u043a\u043b\u0443\u0431.\u0440\u0444", "user@xn--90ard6a.xn--p1ai"},
		{"кириллический домен прописными", "User@\u041a\u041b\u0423\u0411.\u0420\u0424", "user@xn--90ard6a.xn--p1ai"},
		{"A-label как есть", "user@XN--90ARD6A.xn--p1ai", "user@xn--90ard6a.xn--p1ai"},
		{"домен с умлаутом", "anna@m\u00fcnchen.de", "anna@xn--mnchen-3ya.de"},
		// смешанный: кириллическая «а» в латинском имени. Профиль регистрации UTS #46 смешение
		// письменностей не проверяет и принимает; хранится A-label, отличный от example.com,
		// поэтому двойником существующей учётки он не становится.
		{"кириллическая а в латинском домене", "maria.k@ex\u0430mple.com", "maria.k@xn--exmple-4nf.com"},
		// NFKC: полноширинные и лигатура приводятся к ASCII до проверки
		{"лигатура fi в локальной части", "\ufb01nance@example.com", "finance@example.com"},
	}
	for _, c := range cases {
		if got, ok := domain.NormalizeEmail(c.in); !ok || got != c.want {
			t.Errorf("%s: NormalizeEmail(%+q) = %q %v, ждали %q true", c.name, c.in, got, ok, c.want)
		}
	}

	// общий предел считается по хранимой ASCII-форме: «.рф» — 5 байт ввода, «.xn--p1ai» — 9
	idn := func(n int) string {
		return strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." +
			strings.Repeat("d", n) + ".\u0440\u0444"
	}
	if got, ok := domain.NormalizeEmail(idn(52)); !ok || len(got) != 254 || !strings.HasSuffix(got, ".xn--p1ai") {
		t.Errorf("A-форма 254 байта: %q %v", got, ok)
	}
	// 53: ввод 251 байт, A-форма 255
	long := idn(53)
	if len(long) > 254 {
		t.Fatalf("ввод %d байт, ждали не больше 254", len(long))
	}
	if got, ok := domain.NormalizeEmail(long); ok {
		t.Errorf("A-форма длиннее 254 байт принята (ввод %d байт): %q (%d)", len(long), got, len(got))
	}

	bad := []struct{ name, in string }{
		{"не-ASCII в локальной части", "M\u00e1ria@example.com"},
		{"кириллица в локальной части", "\u043c\u0430\u0440\u0438\u044f@example.com"},
		{"кириллическая а в локальной части", "m\u0430ria.k@example.com"},
		{"точка в конце домена", "maria.k@example.com."},
		{"пустая метка домена", "maria.k@example..com"},
		{"подчёркивание в домене", "maria.k@exa_mple.com"},
		{"дефис в начале метки", "maria.k@-example.com"},
		{"битый A-label", "maria.k@xn--zz.com"},
		{"адрес-литерал", "maria.k@[127.0.0.1]"},
		{"эмодзи в домене", "maria.k@exa\u26bdmple.com"},
	}
	for _, c := range bad {
		if got, ok := domain.NormalizeEmail(c.in); ok {
			t.Errorf("%s: NormalizeEmail(%+q) = %q, ждали отказ", c.name, c.in, got)
		}
	}
}

// Ревью Task 5, раунд 2: невидимые и «пустые» символы вне Cf (селекторы вариантов, CGJ,
// хангыльские заполнители, пустой символ Брайля) — ни в локальной части, ни в домене.
func TestNormalizeEmailInvisible(t *testing.T) {
	invisible := map[string]string{
		"U+FE0F селектор варианта": "\ufe0f",
		"U+FE00 селектор варианта": "\ufe00",
		"U+034F CGJ": "\u034f",
		"U+115F хангыльский заполнитель":  "\u115f",
		"U+1160 хангыльский заполнитель":  "\u1160",
		"U+3164 хангыльский заполнитель":  "\u3164",
		"U+FFA0 полуширинный заполнитель": "\uffa0",
		"U+2800 пустой символ Брайля":     "\u2800",
		"U+200B пробел нулевой ширины":    "\u200b",
		"U+00AD мягкий перенос":           "\u00ad",
	}
	for name, x := range invisible {
		for _, in := range []string{
			"mar" + x + "ia.k@example.com",
			x + "maria.k@example.com",
			"maria.k@exa" + x + "mple.com",
			"maria.k@example.com" + x,
		} {
			if got, ok := domain.NormalizeEmail(in); ok {
				t.Errorf("%s: NormalizeEmail(%+q) = %q, ждали отказ", name, in, got)
			}
		}
	}
}
