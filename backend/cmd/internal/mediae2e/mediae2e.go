//go:build wf_e2e

// Package mediae2e — тестовые ручки media для живого e2e (спека media §12a, план media Task 11): только сборка с тегом
// wf_e2e, вне контракта, под префиксом /_e2e/media/. Цели — mediatest.E2EPurposes (e2e_image, e2e_image_private),
// владелец — тип e2e_owner с id из запроса. api: «начать», «завершить», объект, ссылка на закрытое (сессия игрока —
// конвейер public); admin-api: прямая загрузка и снятие (сессия сотрудника — здесь, CSRF — конвейер admin). Ответы —
// JSON по схемам контракта (UploadTicket, MediaImage, PrivateMediaLink), ошибки — Problem с кодами media.*. Прод-образ
// собирается без тега: пакета в нём нет (TestProdBuildWithoutE2ETag, TestProdBuildHasNoE2ERoutes).
package mediae2e

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/media"
	"wf/backend/internal/media/intx"
	"wf/backend/internal/media/mediatest"
	"wf/backend/internal/platform/httpx"
)

// Deps — сервис media, операции в транзакции владельца, пул роли процесса (транзакции ручек), журнал.
type Deps struct {
	Service media.Service
	Tx      *intx.Tx
	Pool    *pgxpool.Pool
	Log     *slog.Logger
}

// purposes — цели тестовых ручек: только тестовые, владелец e2e_owner.
var purposes = []string{mediatest.PurposeImage, mediatest.PurposeImagePrivate}

func owner(id uuid.UUID) media.OwnerRef { return media.OwnerRef{Type: mediatest.OwnerType, ID: id} }

// variantJSON, imageJSON, ticketJSON, linkJSON — схемы MediaVariant, MediaImage, UploadTicket, PrivateMediaLink.
type variantJSON struct {
	URL         string `json:"url,omitempty"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ContentType string `json:"content_type"`
}

type imageJSON struct {
	ID         uuid.UUID              `json:"id"`
	State      string                 `json:"state"`
	RejectCode *string                `json:"reject_code"`
	Variants   map[string]variantJSON `json:"variants"`
}

type ticketJSON struct {
	UploadID       uuid.UUID         `json:"upload_id"`
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	Fields         map[string]string `json:"fields"`
	ExpiresAt      time.Time         `json:"expires_at"`
	CompleteBefore time.Time         `json:"complete_before"`
	MaxBytes       int64             `json:"max_bytes"`
	AcceptedTypes  []string          `json:"accepted_types"`
}

type linkJSON struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// shown — состояние, которое клиент видит изображением (MediaImage.state); прочие (pending_upload, detached,
// taken_down) — «изображения нет».
func shown(s media.State) bool {
	return s == media.StateProcessing || s == media.StateReady || s == media.StateRejected
}

func imageOf(img media.Image) imageJSON {
	out := imageJSON{ID: img.ID, State: string(img.State), Variants: map[string]variantJSON{}}
	if img.RejectCode != "" {
		code := string(img.RejectCode)
		out.RejectCode = &code
	}
	// адрес — как отдал сервис: у закрытого его нет (§3.5), подписанная ссылка — только ручкой private
	for name, v := range img.Variants {
		out.Variants[name] = variantJSON{URL: v.URL, Width: v.Width, Height: v.Height, ContentType: v.ContentType}
	}
	return out
}

func ticketOf(t media.Ticket) ticketJSON {
	return ticketJSON{UploadID: t.UploadID, Method: t.Method, URL: t.URL, Headers: t.Headers, Fields: t.Fields,
		ExpiresAt: t.ExpiresAt.UTC(), CompleteBefore: t.CompleteBefore.UTC(), MaxBytes: t.MaxBytes,
		AcceptedTypes: t.AcceptedTypes}
}

// writeJSON — ответ тестовой ручки: не кэшируется (билет — секрет на 15 минут, ссылка на закрытое — на 5).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errBadRequest — тело или параметр не разбираются, цель не тестовая: 400 request.invalid.
var errBadRequest = httpx.NewError(http.StatusBadRequest, httpx.CodeRequestInvalid)

// decode — JSON без лишних полей; ровно один объект.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			return httpx.NewError(http.StatusRequestEntityTooLarge, httpx.CodeRequestTooLarge)
		}
		return errBadRequest
	}
	if dec.More() {
		return errBadRequest
	}
	return nil
}

func testPurpose(p string) bool { return slices.Contains(purposes, p) }

func notFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusNotFound, httpx.CodeNotFound, "")
}
