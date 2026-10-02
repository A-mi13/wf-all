package i18n_test

import (
	"errors"
	"regexp"
	"slices"
	"testing"
	"testing/fstest"

	"wf/backend/internal/platform/i18n"
	"wf/backend/locales"
)

func catalog(t *testing.T) *i18n.Catalog {
	t.Helper()
	c, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Страж §12.5: ключи ru и en одинаковы.
func TestLocalesHaveSameKeys(t *testing.T) {
	c := catalog(t)
	ru, en := c.Keys("ru"), c.Keys("en")
	if len(ru) == 0 || !slices.Equal(ru, en) {
		t.Fatalf("ключи расходятся:\nru: %v\nen: %v", ru, en)
	}
}

var argRe = regexp.MustCompile(`\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,}]`)

// Аргументы сообщения одинаковы в обоих языках: письмо не теряет подстановку при переводе.
func TestLocalesHaveSameArguments(t *testing.T) {
	raw := map[string]map[string]string{}
	for _, loc := range i18n.Supported {
		m, err := i18n.Flatten(locales.FS, loc)
		if err != nil {
			t.Fatal(err)
		}
		raw[loc] = m
	}
	args := func(s string) []string {
		var out []string
		for _, m := range argRe.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	for k, ru := range raw["ru"] {
		if a, b := args(ru), args(raw["en"][k]); !slices.Equal(a, b) {
			t.Errorf("%s: аргументы ru %v, en %v", k, a, b)
		}
	}
}

func TestPlurals(t *testing.T) {
	c := catalog(t)
	cases := []struct {
		loc   string
		count int
		want  string
	}{
		{"ru", 1, "1 минуту"}, {"ru", 2, "2 минуты"}, {"ru", 5, "5 минут"}, {"ru", 21, "21 минуту"}, {"ru", 15, "15 минут"},
		{"en", 1, "1 minute"}, {"en", 15, "15 minutes"},
	}
	for _, c2 := range cases {
		got, err := c.Text(c2.loc, "common.minutes", map[string]any{"count": c2.count})
		if err != nil || got != c2.want {
			t.Errorf("%s %d: %q %v, нужно %q", c2.loc, c2.count, got, err, c2.want)
		}
	}
}

func TestTextErrorsAndFallback(t *testing.T) {
	c := catalog(t)
	ru, _ := c.Text("ru", "mail.footer", nil)
	if de, err := c.Text("de", "mail.footer", nil); err != nil || de != ru {
		t.Fatalf("неизвестный язык не ушёл в ru: %q %v", de, err)
	}
	if _, err := c.Text("ru", "mail.nope", nil); !errors.Is(err, i18n.ErrUnknownKey) {
		t.Fatalf("неизвестный ключ: %v", err)
	}
	if _, err := c.Text("ru", "common.minutes", nil); err == nil {
		t.Fatal("недостающий аргумент не ошибка — письмо ушло бы с дырой")
	}
}

func TestLoadRejectsBrokenCatalogs(t *testing.T) {
	ok := `{"a": "x"}`
	for name, fsys := range map[string]fstest.MapFS{
		"нет en":    {"ru.json": {Data: []byte(ok)}},
		"не строка": {"ru.json": {Data: []byte(`{"a": 1}`)}, "en.json": {Data: []byte(ok)}},
		"битый ICU": {"ru.json": {Data: []byte(`{"a": "{count, plural, one {x}"}`)}, "en.json": {Data: []byte(ok)}},
		"не JSON":   {"ru.json": {Data: []byte(`{`)}, "en.json": {Data: []byte(ok)}},
	} {
		if _, err := i18n.Load(fsys); err == nil {
			t.Errorf("%s: загружено", name)
		}
	}
}
