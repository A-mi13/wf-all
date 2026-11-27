//go:build wf_e2e

package mediae2e

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
)

// API — тестовые ручки публичного api (монтируются под /_e2e/ за конвейером public: Principal — auth.Middleware,
// Idempotency-Key — idempotency.Middleware). Без сессии игрока — 401.
//
//	POST /_e2e/media/uploads {purpose, owner_id, content_type, bytes} → 201 UploadTicket (NewUpload сам — единственная
//	     транзакция запроса: внутри своей db.InTx его не звать, иначе errSecondTx под Idempotency-Key);
//	POST /_e2e/media/objects {upload_id, owner_id} → 200 MediaImage (ConfirmUpload до транзакции, AttachInTx — в ней);
//	GET  /_e2e/media/objects/{id} → 200 MediaImage (не изображение — 404);
//	GET  /_e2e/media/objects/{id}/private?variant=… → 200 PrivateMediaLink.
func API(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(requirePlayer)
	r.Post("/_e2e/media/uploads", d.startUpload)
	r.Post("/_e2e/media/objects", d.completeUpload)
	r.Get("/_e2e/media/objects/{id}", d.object)
	r.Get("/_e2e/media/objects/{id}/private", d.privateLink)
	r.NotFound(notFound)
	return r
}

func requirePlayer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.From(r.Context()); !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthenticated, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func player(ctx context.Context) media.Uploader {
	p, _ := auth.From(ctx)
	return media.Uploader{UserID: p.UserID}
}

func (d Deps) startUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Purpose     string    `json:"purpose"`
		OwnerID     uuid.UUID `json:"owner_id"`
		ContentType string    `json:"content_type"`
		Bytes       int64     `json:"bytes"`
	}
	if err := decode(r, &in); err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	if !testPurpose(in.Purpose) || in.OwnerID == uuid.Nil {
		httpx.WriteError(d.Log, w, r, errBadRequest)
		return
	}
	t, err := d.Service.NewUpload(r.Context(), in.Purpose, owner(in.OwnerID), player(r.Context()),
		media.Declared{ContentType: in.ContentType, Bytes: in.Bytes})
	if err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ticketOf(t))
}

func (d Deps) completeUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UploadID uuid.UUID `json:"upload_id"`
		OwnerID  uuid.UUID `json:"owner_id"`
	}
	if err := decode(r, &in); err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	if in.UploadID == uuid.Nil || in.OwnerID == uuid.Nil {
		httpx.WriteError(d.Log, w, r, errBadRequest)
		return
	}
	ctx := r.Context()
	// HEAD входящего — сеть, до транзакции владельца (§3.1 п. 5)
	if err := d.Service.ConfirmUpload(ctx, in.UploadID, owner(in.OwnerID), player(ctx)); err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	var img media.Image
	err := db.InTx(ctx, d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		img, err = d.Tx.AttachInTx(ctx, tx, in.UploadID, owner(in.OwnerID), player(ctx))
		return err
	})
	if err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, imageOf(img))
}

func objectID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		notFound(w, r)
		return uuid.Nil, false
	}
	return id, true
}

func (d Deps) object(w http.ResponseWriter, r *http.Request) {
	id, ok := objectID(w, r)
	if !ok {
		return
	}
	img, found, err := d.Service.Image(r.Context(), id)
	if err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	if !found || !shown(img.State) {
		notFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, imageOf(img))
}

func (d Deps) privateLink(w http.ResponseWriter, r *http.Request) {
	id, ok := objectID(w, r)
	if !ok {
		return
	}
	l, err := d.Service.PrivateURL(r.Context(), id, r.URL.Query().Get("variant"))
	if err != nil {
		httpx.WriteError(d.Log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, linkJSON{URL: l.URL, ExpiresAt: l.ExpiresAt.UTC()})
}
