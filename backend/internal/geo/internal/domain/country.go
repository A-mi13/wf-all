package domain

import (
	"slices"
	"strings"
)

// Пределы правил страны (R12, CHECK 0021, схемы GeoCountryCreate/GeoCountryPatch контракта).
const (
	AgeMin       = 1
	AgeMax       = 120
	WeekDayFirst = 1 // понедельник
	WeekDayLast  = 7 // воскресенье
)

// Поля и правила нарушений — те же имена, что у валидатора контракта (FieldError у validation.failed):
// поле — свойство тела без префикса body. (его добавляет вызывающий), правило — ключ схемы.
const (
	FieldCode          = "code"
	FieldCurrency      = "currency"
	FieldDefaultLocale = "default_locale"
	FieldPhonePrefix   = "phone_prefix"
	FieldWeekStartsOn  = "week_starts_on"
	FieldMinSignupAge  = "min_signup_age"
	FieldAgeOfMajority = "age_of_majority"

	RuleFormat  = "format"
	RuleEnum    = "enum"
	RulePattern = "pattern"
	RuleMinimum = "minimum"
	RuleMaximum = "maximum"
)

// CountryRules — правила страны (спека geo §3.1): код, валюта, язык по умолчанию, телефонный код,
// первый день недели и возрастные пороги.
type CountryRules struct {
	Code, Currency, DefaultLocale, PhonePrefix string
	WeekStartsOn, MinSignupAge, AgeOfMajority  int
}

// FieldError — нарушение правила в одном поле: Field — свойство тела (FieldCode …), Code — правило
// (RuleFormat …).
type FieldError struct {
	Field, Code string
}

// ValidateCountry — все нарушения правил страны сразу, в порядке полей (R12): code — действующий
// ISO 3166-1 alpha-2 (countryCodes); currency — действующая денежная единица ISO 4217
// (currencyCodes); default_locale — из SupportedLocales (enum); phone_prefix — «+» и 1–4 цифры без
// ведущего нуля (pattern, как CHECK countries_phone_prefix_format); week_starts_on 1..7; возрасты
// 1..120 (minimum/maximum) и min_signup_age ≤ age_of_majority — иначе age_of_majority/minimum
// (порог совершеннолетия не ниже порога регистрации). Нет нарушений — nil.
func ValidateCountry(r CountryRules) []FieldError {
	var errs []FieldError
	add := func(field, code string) { errs = append(errs, FieldError{Field: field, Code: code}) }
	if !slices.Contains(countryCodes, r.Code) {
		add(FieldCode, RuleFormat)
	}
	if !slices.Contains(currencyCodes, r.Currency) {
		add(FieldCurrency, RuleFormat)
	}
	if !LocaleSupported(r.DefaultLocale) {
		add(FieldDefaultLocale, RuleEnum)
	}
	if !validPhonePrefix(r.PhonePrefix) {
		add(FieldPhonePrefix, RulePattern)
	}
	if code := rangeRule(r.WeekStartsOn, WeekDayFirst, WeekDayLast); code != "" {
		add(FieldWeekStartsOn, code)
	}
	minCode := rangeRule(r.MinSignupAge, AgeMin, AgeMax)
	if minCode != "" {
		add(FieldMinSignupAge, minCode)
	}
	switch majCode := rangeRule(r.AgeOfMajority, AgeMin, AgeMax); {
	case majCode != "":
		add(FieldAgeOfMajority, majCode)
	case minCode == "" && r.MinSignupAge > r.AgeOfMajority:
		add(FieldAgeOfMajority, RuleMinimum)
	}
	return errs
}

// rangeRule — правило, которое нарушает v вне [lo, hi]: minimum, maximum или "" — в пределах.
func rangeRule(v, lo, hi int) string {
	switch {
	case v < lo:
		return RuleMinimum
	case v > hi:
		return RuleMaximum
	}
	return ""
}

// validPhonePrefix — ^\+[1-9][0-9]{0,3}$ без регулярного выражения.
func validPhonePrefix(s string) bool {
	if len(s) < 2 || len(s) > 5 || s[0] != '+' || s[1] < '1' || s[1] > '9' {
		return false
	}
	for i := 2; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// CountryCodes — копия списка действующих кодов ISO 3166-1 alpha-2.
func CountryCodes() []string { return slices.Clone(countryCodes) }

// CurrencyCodes — копия списка действующих денежных единиц ISO 4217.
func CurrencyCodes() []string { return slices.Clone(currencyCodes) }

// countryCodes — 249 официально присвоенных кодов ISO 3166-1 alpha-2. Свой список, а не
// golang.org/x/text/language: IsCountry там принимает исторические коды (SU, YU, CS, AN) — R12.
// Сверено 28.10.2026 со списком ISO 3166-1 (lukes/ISO-3166-Countries-with-Regional-Codes, all.csv).
// XK (Косово, код GeoNames) — пользовательский, не ISO: в список не входит.
var countryCodes = strings.Fields(`
AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ
BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ
CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ
DE DJ DK DM DO DZ
EC EE EG EH ER ES ET
FI FJ FK FM FO FR
GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY
HK HM HN HR HT HU
ID IE IL IM IN IO IQ IR IS IT
JE JM JO JP
KE KG KH KI KM KN KP KR KW KY KZ
LA LB LC LI LK LR LS LT LU LV LY
MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ
NA NC NE NF NG NI NL NO NP NR NU NZ
OM
PA PE PF PG PH PK PL PM PN PR PS PT PW PY
QA
RE RO RS RU RW
SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ
TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ
UA UG UM US UY UZ
VA VC VE VG VI VN VU
WF WS
YE YT
ZA ZM ZW`)

// currencyCodes — действующие денежные единицы ISO 4217 (таблица A.1, list-one.xml SIX от 17.09.2026,
// сверено 28.10.2026). Свой список, а не golang.org/x/text/currency: ParseISO принимает исторические
// коды (RUR, BYR, HRK) и не знает новых (MRU, SLE, VES, VED, ZWG, XCG) — R12. Вне списка намеренно — то, чем
// не платят: фондовые коды (BOV, CHE, CHW, CLF, COU, MXV, USN, UYI, UYW), металлы (XAG, XAU, XPD, XPT),
// расчётные единицы (XAD, XBA–XBD, XDR, XSU, XUA), XTS и XXX.
var currencyCodes = strings.Fields(`
AED AFN ALL AMD AOA ARS AUD AWG AZN
BAM BBD BDT BHD BIF BMD BND BOB BRL BSD BTN BWP BYN BZD
CAD CDF CHF CLP CNY COP CRC CUP CVE CZK
DJF DKK DOP DZD
EGP ERN ETB EUR
FJD FKP
GBP GEL GHS GIP GMD GNF GTQ GYD
HKD HNL HTG HUF
IDR ILS INR IQD IRR ISK
JMD JOD JPY
KES KGS KHR KMF KPW KRW KWD KYD KZT
LAK LBP LKR LRD LSL LYD
MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MYR MZN
NAD NGN NIO NOK NPR NZD
OMR
PAB PEN PGK PHP PKR PLN PYG
QAR
RON RSD RUB RWF
SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL
THB TJS TMT TND TOP TRY TTD TWD TZS
UAH UGX USD UYU UZS
VED VES VND VUV
WST
XAF XCD XCG XOF XPF
YER
ZAR ZMW ZWG`)
