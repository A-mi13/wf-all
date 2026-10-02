package i18n_test

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/kaptinlin/messageformat-go/mf1"

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

// messageArgs — имена аргументов сообщения из AST парсера mf1 (а не регуляркой по тексту:
// слова вариантов select/plural вроде {Captain} не аргументы).
func messageArgs(t *testing.T, src string) []string {
	t.Helper()
	toks, err := mf1.Parse(src, nil)
	if err != nil {
		t.Fatalf("разбор %q: %v", src, err)
	}
	set := map[string]bool{}
	var walk func([]mf1.Token)
	walk = func(toks []mf1.Token) {
		for _, tok := range toks {
			switch v := tok.(type) {
			case *mf1.PlainArg:
				set[v.Arg] = true
			case *mf1.FunctionArg:
				set[v.Arg] = true
				walk(v.Param)
			case *mf1.Select:
				set[v.Arg] = true
				for _, c := range v.Cases {
					walk(c.Tokens)
				}
			}
		}
	}
	walk(toks)
	return slices.Sorted(maps.Keys(set))
}

func TestMessageArgs(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{"{role, select, captain {Captain} other {Player}}", []string{"role"}},
		{"{role, select, captain {Капитан} other {Игрок}}", []string{"role"}},
		{"Привет, {name}! {count, plural, one {# минуту} other {# минуты}}", []string{"count", "name"}},
		{"{n, plural, one {{who} one} other {{who} many}}", []string{"n", "who"}},
		{"{d, date, short} {x, number}", []string{"d", "x"}},
		{"без аргументов", nil},
	}
	for _, c := range cases {
		if got := messageArgs(t, c.src); !slices.Equal(got, c.want) {
			t.Errorf("%q: %v, нужно %v", c.src, got, c.want)
		}
	}
	// расхождение имён аргументов между языками ловится
	if slices.Equal(messageArgs(t, "{num, plural, other {#}}"), messageArgs(t, "{count, plural, other {#}}")) {
		t.Error("разные имена аргументов не различаются")
	}
}

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
	for k, ru := range raw["ru"] {
		if a, b := messageArgs(t, ru), messageArgs(t, raw["en"][k]); !slices.Equal(a, b) {
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
	ru, err := c.Text("ru", "mail.footer", nil)
	if err != nil {
		t.Fatal(err)
	}
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
		"ключ с точкой совпал с вложенным": {"ru.json": {Data: []byte(`{"mail.footer": "x", "mail": {"footer": "y"}}`)}, "en.json": {Data: []byte(ok)}},
		"не JSON": {"ru.json": {Data: []byte(`{`)}, "en.json": {Data: []byte(ok)}},
	} {
		if _, err := i18n.Load(fsys); err == nil {
			t.Errorf("%s: загружено", name)
		}
	}
}
