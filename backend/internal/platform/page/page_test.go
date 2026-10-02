package page_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/page"
)

func TestCursorRoundTrip(t *testing.T) {
	c := page.Cursor{CreatedAt: time.Date(2026, 10, 2, 12, 0, 0, 123456789, time.UTC), ID: uuid.New()}
	s := page.Encode(c)
	if strings.ContainsAny(s, "+/=") {
		t.Fatalf("курсор не годится для query без экранирования: %q", s)
	}
	got, err := page.Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	// точность базы — микросекунды: наносекунды курсор не переносит
	if !got.CreatedAt.Equal(c.CreatedAt.Truncate(time.Microsecond)) || got.ID != c.ID {
		t.Fatalf("%+v != %+v", got, c)
	}
}

func TestDecodeRejects(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	for name, s := range map[string]string{
		"мусор":        "%%%",
		"не JSON":      enc([]byte("nope")),
		"чужая версия": enc([]byte(`{"v":2,"t":1,"id":"` + uuid.NewString() + `"}`)),
		"без id":       enc([]byte(`{"v":1,"t":1}`)),
		"пусто":        "",
	} {
		_, err := page.Decode(s)
		if !errors.Is(err, page.ErrBadCursor) {
			t.Errorf("%s: %v", name, err)
		}
	}
	pe, ok := errors.AsType[httpx.ProblemError](page.ErrBadCursor)
	if !ok {
		t.Fatal("ErrBadCursor не ProblemError")
	}
	p := pe.Problem()
	if p.Status != http.StatusBadRequest || p.Code != httpx.CodeValidationFailed ||
		len(p.Errors) != 1 || p.Errors[0] != (httpx.FieldError{Field: "query.cursor", Code: "format"}) {
		t.Fatalf("%+v", p)
	}
}

func TestLimit(t *testing.T) {
	n := func(v int) *int { return &v }
	cases := []struct {
		in   *int
		want int
	}{{nil, 20}, {n(0), 20}, {n(-5), 20}, {n(1), 1}, {n(100), 100}, {n(500), 100}}
	for _, c := range cases {
		if got := page.Limit(c.in); got != c.want {
			t.Errorf("Limit(%v) = %d, нужно %d", c.in, got, c.want)
		}
	}
}
