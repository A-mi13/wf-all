//go:build wf_e2e

package mediae2e

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/peer"
)

// DirectMax — предел тела прямой загрузки (спека §3.6: 5 МБ у операции, исключение из общего лимита, R16).
const DirectMax = 5 << 20

// DirectPath — путь прямой загрузки (для httpx.BodyLimit сборки admin-api).
const DirectPath = "/_e2e/media/direct"

// takeDownReason — причина снятия тестовой ручкой (код, не текст: intx.TakeDownInTx).
const takeDownReason = "e2e.take_down"

// AdminDeps — Deps и сессия сотрудника: загрузчик, имя cookie (admin.CookieName) и подпись для аудита снятия.
type AdminDeps struct {
	Deps
	Sessions auth.StaffSessions
	Cookie   string
	Label    func(ctx context.Context, s *auth.Staff) (string, error)
}

// Admin — тестовые ручки admin-api (под /_e2e/ за конвейером admin: аллоулист, CSRF и лимит тела — там). Звено сессии
// конвейера идёт только по маршрутам контракта, поэтому сессию сотрудника (активную, без Reauth) ставит эта ручка, за ней —
// идемпотентность (staff_idempotency_keys): ровно одна db.InTx на запрос.
//
//	POST /_e2e/media/direct (multipart: purpose, owner_id, content_type, file последним) → 201 MediaImage
//	     (Stage — без базы, CreateDirectInTx — в транзакции);
//	POST /_e2e/media/objects/{id}/take-down → 204 (TakeDownInTx с аудитом и событием).
func Admin(d AdminDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(d.requireStaff, idempotency.Middleware(d.Pool, idempotency.Config{Table: idempotency.Staff}, d.Log))
	r.Post(DirectPath, d.direct)
	r.Post("/_e2e/media/objects/{id}/take-down", d.takeDown)
	r.NotFound(notFound)
	return r
}

func (d AdminDeps) requireStaff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(d.Cookie)
		if err != nil || c.Value == "" {
			httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthenticated, "")
			return
		}
		st, err := d.Sessions.LoadStaff(r.Context(), c.Value, peer.From(r.Context()).IP, false)
		switch {
		case errors.Is(err, auth.ErrSessionInvalid), errors.Is(err, auth.ErrSessionReplaced),
			err == nil && (st == nil || st.Stage != "active" || st.Reauth):
			httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthenticated, "")
			return
		case err != nil:
			httpx.WriteError(d.Log, w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithStaff(r.Context(), st)))
	})
}

func staffOf(ctx context.Context) *auth.Staff {
	st, _ := auth.StaffFrom(ctx)
	return st
}

// direct — поля формы до файла, файл последним (как у формы S3); тип — из поля content_type.
func (d AdminDeps) direct(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		httpx.WriteError(d.Log, w, r, errBadRequest)
		return
	}
	fields := map[string]string{}
	ctx := r.Context()
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			httpx.WriteError(d.Log, w, r, readErr(err))
			return
		}
		name := part.FormName()
		if name != "file" {
			v, err := io.ReadAll(io.LimitReader(part, 1<<10))
			if err != nil {
				httpx.WriteError(d.Log, w, r, readErr(err))
				return
			}
			fields[name] = strings.TrimSpace(string(v))
			continue
		}
		ownerID, err := uuid.Parse(fields["owner_id"])
		if err != nil || !testPurpose(fields["purpose"]) {
			httpx.WriteError(d.Log, w, r, errBadRequest)
			return
		}
		staged, err := d.Service.Stage(ctx, fields["purpose"], part, media.Declared{ContentType: fields["content_type"]})
		if err != nil {
			httpx.WriteError(d.Log, w, r, readErr(err))
			return
		}
		if _, err := mr.NextPart(); !errors.Is(err, io.EOF) {
			// файл не последним: объект не создаётся, входящее уберёт чистка (§3.9)
			httpx.WriteError(d.Log, w, r, errBadRequest)
			return
		}
		var img media.Image
		err = db.InTx(ctx, d.Pool, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			img, err = d.Tx.CreateDirectInTx(ctx, tx, staged, owner(ownerID), media.Uploader{StaffID: staffOf(ctx).StaffID})
			return err
		})
		if err != nil {
			httpx.WriteError(d.Log, w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, imageOf(img))
		return
	}
	httpx.WriteError(d.Log, w, r, errBadRequest)
}

// readErr — чтение тела сверх предела (R16) — 413, иначе как есть.
func readErr(err error) error {
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		return httpx.NewError(http.StatusRequestEntityTooLarge, httpx.CodeRequestTooLarge)
	}
	return err
}

func (d AdminDeps) takeDown(w http.ResponseWriter, r *http.Request) {
	id, ok := objectID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	st := staffOf(ctx)
	// подпись сотрудника — до транзакции (чтение учётки и сессии, как у подтверждений)
	label, err := d.Label(ctx, st)
	if err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	err = db.InTx(ctx, d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		return d.Tx.TakeDownInTx(ctx, tx, id, media.Actor{StaffID: st.StaffID, Role: st.Role, Label: label}, takeDownReason)
	})
	if err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
