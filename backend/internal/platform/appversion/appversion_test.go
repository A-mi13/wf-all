package appversion_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/platform/appversion"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.10.0", "1.9.9", 1}, // строкой было бы наоборот
		{"2.0.0", "10.0.0", -1},
		{"0.0.0", "0.0.1", -1},
		{"1.0.10", "1.0.9", 1},
		{"18446744073709551615.0.0", "1.0.0", 1}, // максимум uint64
	}
	for _, c := range cases {
		got, err := appversion.Compare(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("Compare(%q, %q) = %d, %v; нужно %d", c.a, c.b, got, err, c.want)
		}
	}
}

func TestCompareRejects(t *testing.T) {
	for _, bad := range []string{
		"", "1.2", "1.2.3.4", "1.2.3+45", "1.2.3-beta", "v1.2.3", " 1.2.3", "1.2.3 ", "1..3",
		"01.2.3", "1.02.3", "1.2.03", "1.2.x", "-1.2.3", "+1.2.3", "١.٢.٣", "18446744073709551616.0.0",
	} {
		if _, err := appversion.Compare(bad, "1.0.0"); !errors.Is(err, appversion.ErrFormat) {
			t.Errorf("Compare(%q, …): %v, нужен ErrFormat", bad, err)
		}
		if _, err := appversion.Compare("1.0.0", bad); !errors.Is(err, appversion.ErrFormat) {
			t.Errorf("Compare(…, %q): %v, нужен ErrFormat", bad, err)
		}
	}
}

func valid(p appversion.Platform) appversion.Versions {
	url := "https://apps.apple.com/app/id1234567890"
	if p == appversion.Android {
		url = "https://play.google.com/store/apps/details?id=app.wf"
	}
	return appversion.Versions{Platform: p, Min: "1.2.3", Recommended: "1.10.0", StoreURL: url}
}

func TestValidateAccepts(t *testing.T) {
	for _, v := range []appversion.Versions{
		valid(appversion.IOS),
		valid(appversion.Android),
		func() appversion.Versions { v := valid(appversion.IOS); v.Recommended = v.Min; return v }(), // min = recommended
	} {
		if err := appversion.Validate(v); err != nil {
			t.Errorf("%+v: %v", v, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(v *appversion.Versions)
		field string
		code  string
	}{
		{"неизвестная платформа", func(v *appversion.Versions) { v.Platform = "web" }, "platform", appversion.CodeEnum},
		{"пустая платформа", func(v *appversion.Versions) { v.Platform = "" }, "platform", appversion.CodeEnum},
		{"min без патча", func(v *appversion.Versions) { v.Min = "1.2" }, "min_version", appversion.CodeFormat},
		{"recommended со сборкой", func(v *appversion.Versions) { v.Recommended = "1.10.0+45" }, "recommended_version", appversion.CodeFormat},
		{"min выше recommended", func(v *appversion.Versions) { v.Min, v.Recommended = "1.10.0", "1.9.0" }, "recommended_version", appversion.CodeMinimum},
		{"http", func(v *appversion.Versions) { v.StoreURL = "http://apps.apple.com/app/id1" }, "store_url", appversion.CodeFormat},
		{"схема в верхнем регистре", func(v *appversion.Versions) { v.StoreURL = "HTTPS://apps.apple.com/app/id1" }, "store_url", appversion.CodeFormat}, // CHECK базы: '^https://'
		{"без схемы", func(v *appversion.Versions) { v.StoreURL = "apps.apple.com/app/id1" }, "store_url", appversion.CodeFormat},
		{"пусто", func(v *appversion.Versions) { v.StoreURL = "" }, "store_url", appversion.CodeFormat},
		{"opaque", func(v *appversion.Versions) { v.StoreURL = "https:apps.apple.com" }, "store_url", appversion.CodeFormat},
		{"userinfo", func(v *appversion.Versions) { v.StoreURL = "https://apps.apple.com@evil.example/x" }, "store_url", appversion.CodeFormat},
		{"стор чужой платформы", func(v *appversion.Versions) { v.StoreURL = "https://play.google.com/store/apps/details?id=app.wf" }, "store_url", appversion.CodeHost},
		{"суффикс хоста", func(v *appversion.Versions) { v.StoreURL = "https://apps.apple.com.evil.example/x" }, "store_url", appversion.CodeHost},
		{"хост в пути", func(v *appversion.Versions) { v.StoreURL = "https://evil.example/apps.apple.com" }, "store_url", appversion.CodeHost},
		{"порт", func(v *appversion.Versions) { v.StoreURL = "https://apps.apple.com:8443/app/id1" }, "store_url", appversion.CodeHost},
	}
	for _, c := range cases {
		v := valid(appversion.IOS)
		c.edit(&v)
		err := appversion.Validate(v)
		ve, ok := errors.AsType[appversion.ValidationError](err)
		if !ok || !slices.Contains(ve, appversion.FieldError{Field: c.field, Code: c.code}) {
			t.Errorf("%s: %v, нужно %s: %s", c.name, err, c.field, c.code)
			continue
		}
		if !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: в тексте ошибки нет поля: %q", c.name, err)
		}
	}

	// android проверяется своим хостом
	v := valid(appversion.Android)
	v.StoreURL = "https://apps.apple.com/app/id1"
	if ve, ok := errors.AsType[appversion.ValidationError](appversion.Validate(v)); !ok ||
		!slices.Contains(ve, appversion.FieldError{Field: "store_url", Code: appversion.CodeHost}) {
		t.Errorf("android с хостом App Store принят: %v", ve)
	}

	// все нарушения сразу — админка подсвечивает каждое поле
	v = valid(appversion.IOS)
	v.Min, v.StoreURL = "1.2", "http://x"
	ve, ok := errors.AsType[appversion.ValidationError](appversion.Validate(v))
	if !ok || len(ve) != 2 {
		t.Fatalf("два нарушения: %v", ve)
	}
}
