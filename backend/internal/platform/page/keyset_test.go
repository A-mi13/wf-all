package page_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/page"
)

// Набор полей списка городов geo: ранг статуса, население, id (план geo, Task 5).
var cityKinds = []page.Kind{page.KindInt64, page.KindInt64, page.KindUUID}

// sameValues — значения курсора совпали; время — по Equal и обязательно в UTC.
func sameValues(want, got []any) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if tw, ok := want[i].(time.Time); ok {
			tg, ok := got[i].(time.Time)
			if !ok || !tw.Equal(tg) || tg.Location() != time.UTC {
				return false
			}
			continue
		}
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

func TestKeysetRoundTrip(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	id := uuid.New()
	cases := []struct {
		name  string
		kinds []page.Kind
		vals  []any
		want  []any // nil — равно vals
	}{
		{"города", cityKinds, []any{int64(2), int64(433931), id}, nil},
		{"границы int64", []page.Kind{page.KindInt64, page.KindInt64, page.KindInt64},
			[]any{int64(math.MinInt64), int64(math.MaxInt64), int64(1<<53 + 1)}, nil},
		{"строки", []page.Kind{page.KindString, page.KindString, page.KindString},
			[]any{"", "Ставрополь «центр», \"кавычки\" \\ / \x00 <&>", strings.Repeat("я", 300)}, nil},
		// точность базы — микросекунды; зона отбрасывается, момент сохраняется
		{"время с наносекундами в чужой зоне", []page.Kind{page.KindTime, page.KindUUID},
			[]any{time.Date(2026, 10, 2, 23, 30, 0, 123456789, msk), uuid.Nil},
			[]any{time.Date(2026, 10, 2, 20, 30, 0, 123456000, time.UTC), uuid.Nil}},
		{"время до эпохи Unix", []page.Kind{page.KindTime},
			[]any{time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)}, nil},
	}
	for _, c := range cases {
		s := page.EncodeKeyset(page.Keyset{Set: "test.list", Values: c.vals}, c.kinds...)
		if strings.ContainsAny(s, "+/=") {
			t.Errorf("%s: курсор не годится для query без экранирования: %q", c.name, s)
		}
		got, err := page.DecodeKeyset(s, "test.list", c.kinds...)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		want := c.want
		if want == nil {
			want = c.vals
		}
		if got.Set != "test.list" || !sameValues(want, got.Values) {
			t.Errorf("%s: %+v, нужно %v", c.name, got, want)
		}
	}
}

// rewrite — правка JSON внутри курсора: курсор с верным отпечатком, но порченым телом.
func rewrite(t *testing.T, s string, edit func(w map[string]any)) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	var w map[string]any
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	edit(w)
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func setK(i int, v any) func(map[string]any) {
	return func(w map[string]any) { w["k"].([]any)[i] = v }
}

func TestDecodeKeysetRejects(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	id := uuid.New()
	good := page.EncodeKeyset(page.Keyset{Set: "geo.cities(rank,population,id)", Values: []any{int64(2), int64(433931), id}}, cityKinds...)
	// rewrite без правок сохраняет курсор годным — иначе все случаи ниже проверяли бы вхолостую
	if _, err := page.DecodeKeyset(rewrite(t, good, func(map[string]any) {}), "geo.cities(rank,population,id)", cityKinds...); err != nil {
		t.Fatalf("rewrite портит курсор сам: %v", err)
	}
	cases := map[string]string{
		"мусор":       "%%%",
		"пусто":       "",
		"не JSON":     enc([]byte("nope")),
		"обрезан":     good[:len(good)/2],
		"курсор v1":   page.Encode(page.Cursor{CreatedAt: time.Now(), ID: id}),
		"чужой набор": page.EncodeKeyset(page.Keyset{Set: "teams.list", Values: []any{int64(2), int64(433931), id}}, cityKinds...),
		"чужие типы полей": page.EncodeKeyset(page.Keyset{Set: "geo.cities(rank,population,id)", Values: []any{int64(2), int64(433931), int64(7)}},
			page.KindInt64, page.KindInt64, page.KindInt64),
		// здесь решает только отпечаток типов: без Kinds в нём "2" разобралось бы как int64
		"строка на месте int64": page.EncodeKeyset(page.Keyset{Set: "geo.cities(rank,population,id)", Values: []any{"2", int64(433931), id}},
			page.KindString, page.KindInt64, page.KindUUID),
		"версия 3":          rewrite(t, good, func(w map[string]any) { w["v"] = 3 }),
		"версия 1":          rewrite(t, good, func(w map[string]any) { w["v"] = 1 }),
		"без отпечатка":     rewrite(t, good, func(w map[string]any) { delete(w, "f") }),
		"лишнее значение":   rewrite(t, good, func(w map[string]any) { w["k"] = append(w["k"].([]any), "1") }),
		"не хватает":        rewrite(t, good, func(w map[string]any) { w["k"] = w["k"].([]any)[:2] }),
		"значений нет":      rewrite(t, good, func(w map[string]any) { w["k"] = nil }),
		"число, не строка":  rewrite(t, good, setK(0, 2)),
		"не число в int64":  rewrite(t, good, setK(1, "433931x")),
		"дробь в int64":     rewrite(t, good, setK(0, "2.5")),
		"переполнение":      rewrite(t, good, setK(1, "9223372036854775808")),
		"не uuid":           rewrite(t, good, setK(2, "not-a-uuid")),
		"пустая строка int": rewrite(t, good, setK(0, "")),
	}
	for name, s := range cases {
		if _, err := page.DecodeKeyset(s, "geo.cities(rank,population,id)", cityKinds...); !errors.Is(err, page.ErrBadCursor) {
			t.Errorf("%s: %v, нужен ErrBadCursor", name, err)
		}
	}

	tc := page.EncodeKeyset(page.Keyset{Set: "t.time", Values: []any{time.Now()}}, page.KindTime)
	if _, err := page.DecodeKeyset(rewrite(t, tc, setK(0, "yesterday")), "t.time", page.KindTime); !errors.Is(err, page.ErrBadCursor) {
		t.Errorf("время не числом: %v", err)
	}
}

// v1 и v2 не путаются: Decode отвергает курсор v2 той же ошибкой 400.
func TestDecodeRejectsKeysetCursor(t *testing.T) {
	s := page.EncodeKeyset(page.Keyset{Set: "geo.cities(rank,population,id)", Values: []any{int64(2), int64(1), uuid.New()}}, cityKinds...)
	if _, err := page.Decode(s); !errors.Is(err, page.ErrBadCursor) {
		t.Fatalf("Decode принял курсор v2: %v", err)
	}
}

// Ошибка вызывающего кода — паника, а не тихо испорченный курсор.
func TestKeysetMisusePanics(t *testing.T) {
	id := uuid.New()
	cases := map[string]func(){
		"нет полей":  func() { page.EncodeKeyset(page.Keyset{Set: "t"}) },
		"нет набора": func() { page.EncodeKeyset(page.Keyset{Values: []any{int64(1)}}, page.KindInt64) },
		"значений меньше полей": func() {
			page.EncodeKeyset(page.Keyset{Set: "t", Values: []any{int64(1)}}, page.KindInt64, page.KindUUID)
		},
		"значений больше полей": func() { page.EncodeKeyset(page.Keyset{Set: "t", Values: []any{int64(1), id}}, page.KindInt64) },
		"int вместо int64":      func() { page.EncodeKeyset(page.Keyset{Set: "t", Values: []any{1}}, page.KindInt64) },
		"строка вместо uuid":    func() { page.EncodeKeyset(page.Keyset{Set: "t", Values: []any{id.String()}}, page.KindUUID) },
		"Kind 0":                func() { page.EncodeKeyset(page.Keyset{Set: "t", Values: []any{int64(1)}}, page.Kind(0)) },
		"Kind 99":               func() { page.EncodeKeyset(page.Keyset{Set: "t", Values: []any{int64(1)}}, page.Kind(99)) },
		"Decode без полей":      func() { _, _ = page.DecodeKeyset("x", "t") },
		"Decode без набора":     func() { _, _ = page.DecodeKeyset("x", "", page.KindInt64) },
	}
	for name, f := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: нет паники", name)
				}
			}()
			f()
		}()
	}
}
