package domain_test

import (
	"slices"
	"testing"

	"golang.org/x/text/currency"
	"golang.org/x/text/language"

	"wf/backend/internal/geo/internal/domain"
)

func validRU() domain.CountryRules {
	return domain.CountryRules{
		Code: "RU", Currency: "RUB", DefaultLocale: "ru", PhonePrefix: "+7",
		WeekStartsOn: 1, MinSignupAge: 14, AgeOfMajority: 18,
	}
}

func TestValidateCountry(t *testing.T) {
	cases := []struct {
		name string
		edit func(*domain.CountryRules)
		want []domain.FieldError
	}{
		{"RU/RUB/ru/+7 — годится", func(*domain.CountryRules) {}, nil},
		{"KZ/KZT/ru/+7 — годится", func(r *domain.CountryRules) { r.Code, r.Currency = "KZ", "KZT" }, nil},
		{"BY/BYN/en/+375 — годится", func(r *domain.CountryRules) {
			r.Code, r.Currency, r.DefaultLocale, r.PhonePrefix = "BY", "BYN", "en", "+375"
		}, nil},
		{"XX — не страна", func(r *domain.CountryRules) { r.Code = "XX" }, []domain.FieldError{{Field: "code", Code: "format"}}},
		{"QZ — частный код", func(r *domain.CountryRules) { r.Code = "QZ" }, []domain.FieldError{{Field: "code", Code: "format"}}},
		{"SU — исторический код (СССР)", func(r *domain.CountryRules) { r.Code = "SU" }, []domain.FieldError{{Field: "code", Code: "format"}}},
		{"YU — исторический код", func(r *domain.CountryRules) { r.Code = "YU" }, []domain.FieldError{{Field: "code", Code: "format"}}},
		{"EU — не страна", func(r *domain.CountryRules) { r.Code = "EU" }, []domain.FieldError{{Field: "code", Code: "format"}}},
		{"ru — нижний регистр", func(r *domain.CountryRules) { r.Code = "ru" }, []domain.FieldError{{Field: "code", Code: "format"}}},
		{"RUR — историческая валюта", func(r *domain.CountryRules) { r.Currency = "RUR" }, []domain.FieldError{{Field: "currency", Code: "format"}}},
		{"HRK — выведена в 2023", func(r *domain.CountryRules) { r.Currency = "HRK" }, []domain.FieldError{{Field: "currency", Code: "format"}}},
		{"XAU — не денежная единица", func(r *domain.CountryRules) { r.Currency = "XAU" }, []domain.FieldError{{Field: "currency", Code: "format"}}},
		{"rub — нижний регистр", func(r *domain.CountryRules) { r.Currency = "rub" }, []domain.FieldError{{Field: "currency", Code: "format"}}},
		{"de — не поддерживается", func(r *domain.CountryRules) { r.DefaultLocale = "de" }, []domain.FieldError{{Field: "default_locale", Code: "enum"}}},
		{"7 без плюса", func(r *domain.CountryRules) { r.PhonePrefix = "7" }, []domain.FieldError{{Field: "phone_prefix", Code: "pattern"}}},
		{"+0", func(r *domain.CountryRules) { r.PhonePrefix = "+0" }, []domain.FieldError{{Field: "phone_prefix", Code: "pattern"}}},
		{"+12345 — 5 цифр", func(r *domain.CountryRules) { r.PhonePrefix = "+12345" }, []domain.FieldError{{Field: "phone_prefix", Code: "pattern"}}},
		{"+1234 — 4 цифры годится", func(r *domain.CountryRules) { r.PhonePrefix = "+1234" }, nil},
		{"неделя 0", func(r *domain.CountryRules) { r.WeekStartsOn = 0 }, []domain.FieldError{{Field: "week_starts_on", Code: "minimum"}}},
		{"неделя 8", func(r *domain.CountryRules) { r.WeekStartsOn = 8 }, []domain.FieldError{{Field: "week_starts_on", Code: "maximum"}}},
		{"неделя 7 годится", func(r *domain.CountryRules) { r.WeekStartsOn = 7 }, nil},
		{"min 0", func(r *domain.CountryRules) { r.MinSignupAge = 0 }, []domain.FieldError{{Field: "min_signup_age", Code: "minimum"}}},
		{"majority 121", func(r *domain.CountryRules) { r.AgeOfMajority = 121 }, []domain.FieldError{{Field: "age_of_majority", Code: "maximum"}}},
		{"min 18 > majority 16", func(r *domain.CountryRules) { r.MinSignupAge, r.AgeOfMajority = 18, 16 },
			[]domain.FieldError{{Field: "age_of_majority", Code: "minimum"}}},
		{"min = majority годится", func(r *domain.CountryRules) { r.MinSignupAge, r.AgeOfMajority = 18, 18 }, nil},
		{"все нарушения сразу", func(r *domain.CountryRules) {
			*r = domain.CountryRules{Code: "XX", Currency: "RUR", DefaultLocale: "de", PhonePrefix: "7",
				WeekStartsOn: 9, MinSignupAge: 30, AgeOfMajority: 20}
		}, []domain.FieldError{
			{Field: "code", Code: "format"}, {Field: "currency", Code: "format"},
			{Field: "default_locale", Code: "enum"}, {Field: "phone_prefix", Code: "pattern"},
			{Field: "week_starts_on", Code: "maximum"}, {Field: "age_of_majority", Code: "minimum"},
		}},
	}
	for _, c := range cases {
		r := validRU()
		c.edit(&r)
		if got := domain.ValidateCountry(r); !slices.Equal(got, c.want) {
			t.Errorf("%s: ValidateCountry = %v, ждали %v", c.name, got, c.want)
		}
	}
}

// TestCountryCodesList — список ISO 3166-1: 249 официально присвоенных кодов, каждый — страна по
// данным CLDR (x/text) и не частный код. Исторические (SU, YU, CS, AN) в списке не нужны.
func TestCountryCodesList(t *testing.T) {
	codes := domain.CountryCodes()
	if len(codes) != 249 {
		t.Fatalf("кодов ISO 3166-1 = %d, ждали 249", len(codes))
	}
	for _, c := range codes {
		r, err := language.ParseRegion(c)
		if err != nil || !r.IsCountry() || r.IsPrivateUse() || r.String() != c {
			t.Errorf("%s: не страна по CLDR (err=%v)", c, err)
		}
	}
	for _, old := range []string{"SU", "YU", "CS", "AN", "DD", "ZR", "TP", "BU", "XK", "UK"} {
		if slices.Contains(codes, old) {
			t.Errorf("исторический или неофициальный код %s в списке", old)
		}
	}
	codes[0] = "ZZ"
	if slices.Contains(domain.CountryCodes(), "ZZ") {
		t.Fatal("CountryCodes отдаёт общий срез — правка снаружи меняет список")
	}
}

// TestCurrencyCodesList — действующие денежные единицы ISO 4217: каждая известна CLDR, кроме
// введённых после его данных (x/text не знает MRU, SLE, VES, VED, ZWG, XCG — потому и свой список).
func TestCurrencyCodesList(t *testing.T) {
	codes := domain.CurrencyCodes()
	newer := []string{"MRU", "SLE", "VED", "VES", "XCG", "ZWG"}
	for _, c := range codes {
		if slices.Contains(newer, c) {
			continue
		}
		if _, err := currency.ParseISO(c); err != nil {
			t.Errorf("%s: неизвестна CLDR: %v", c, err)
		}
	}
	for _, c := range append([]string{"RUB", "KZT", "BYN", "USD", "EUR", "XAF", "XOF", "XPF", "XCD"}, newer...) {
		if !slices.Contains(codes, c) {
			t.Errorf("действующая валюта %s не в списке", c)
		}
	}
	for _, old := range []string{"RUR", "SUR", "BYR", "HRK", "ANG", "CUC", "SLL", "ZWL", "VEF", "MRO", "STD",
		"XXX", "XTS", "XAU", "XAG", "XDR", "XAD", "BOV", "USN", "CHE", "CHW", "CLF", "COU", "MXV", "UYI", "UYW"} {
		if slices.Contains(codes, old) {
			t.Errorf("недействующий код или не денежная единица %s в списке", old)
		}
	}
	codes[0] = "ZZZ"
	if slices.Contains(domain.CurrencyCodes(), "ZZZ") {
		t.Fatal("CurrencyCodes отдаёт общий срез")
	}
}
