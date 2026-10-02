// Package page — keyset-пагинация по (created_at, id) (спека бэкенда §6.9): непрозрачный
// курсор, limit по умолчанию 20, максимум 100.
package page

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/httpx"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
	version      = 1
)

// Cursor — последняя строка страницы.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type wire struct {
	V  int       `json:"v"`
	T  int64     `json:"t"` // created_at, микросекунды Unix
	ID uuid.UUID `json:"id"`
}

type badCursor struct{}

func (badCursor) Error() string { return "page: недействительный курсор" }

func (badCursor) Problem() httpx.Problem {
	return httpx.Problem{Status: http.StatusBadRequest, Code: httpx.CodeValidationFailed,
		Errors: []httpx.FieldError{{Field: "query.cursor", Code: "format"}}}
}

// ErrBadCursor — курсор не разбирается: 400 validation.failed по полю query.cursor.
var ErrBadCursor httpx.ProblemError = badCursor{}

// Encode — непрозрачная строка курсора (base64url без заполнения): клиент её не собирает
// и не полагается на формат.
func Encode(c Cursor) string {
	b, _ := json.Marshal(wire{V: version, T: c.CreatedAt.UnixMicro(), ID: c.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode — курсор из строки; любая негодная — ErrBadCursor.
func Decode(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) == 0 {
		return Cursor{}, ErrBadCursor
	}
	var w wire
	if json.Unmarshal(raw, &w) != nil || w.V != version || w.ID == uuid.Nil {
		return Cursor{}, ErrBadCursor
	}
	return Cursor{CreatedAt: time.UnixMicro(w.T).UTC(), ID: w.ID}, nil
}

// Limit — размер страницы из запроса: нет или ≤ 0 — 20, больше 100 — 100.
func Limit(requested *int) int {
	switch {
	case requested == nil || *requested <= 0:
		return DefaultLimit
	case *requested > MaxLimit:
		return MaxLimit
	}
	return *requested
}
