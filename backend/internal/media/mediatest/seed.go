package mediatest

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/id"
)

// Execer — пул или транзакция владельца базы (посев в обход прав ролей).
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Seed — объект для посева: поля по CHECK 0028 выводятся из State (у pending_upload — билет на Intended со сроком
// ExpiresAt; у ready — варианты Variants с ключами <id>/<имя>.webp).
type Seed struct {
	Purpose    string
	Visibility media.Visibility
	State      media.State
	Owner      media.OwnerRef // кроме pending_upload
	Intended   media.OwnerRef // только pending_upload
	By         media.Uploader
	At         time.Time
	ExpiresAt  time.Time
	Variants   []string
}

func nullable(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}

// SeedObject — вставка объекта (и билета, вариантов) SQL-ом; id — новый UUIDv7.
func SeedObject(ctx context.Context, db Execer, s Seed) (uuid.UUID, error) {
	oid := id.New()
	var ownerType, ownerID, uploaded, ready, ended, reject any = s.Owner.Type, s.Owner.ID, s.At, nil, nil, nil
	switch s.State {
	case media.StatePendingUpload:
		ownerType, ownerID, uploaded = nil, nil, nil
	case media.StateReady:
		ready = s.At
	case media.StateRejected:
		ended, reject = s.At, string(media.RejectCorrupt)
	case media.StateDetached, media.StateTakenDown:
		ended = s.At
	}
	if _, err := db.Exec(ctx, `INSERT INTO media_objects (id, purpose, visibility, state, owner_type, owner_id,
		uploaded_by_user, uploaded_by_staff, declared_type, declared_bytes, created_at, uploaded_at, ready_at, ended_at,
		reject_code) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'image/png', 10, $9, $10, $11, $12, $13)`,
		oid, s.Purpose, string(s.Visibility), string(s.State), ownerType, ownerID, nullable(s.By.UserID),
		nullable(s.By.StaffID), s.At, uploaded, ready, ended, reject); err != nil {
		return uuid.Nil, err
	}
	if s.State == media.StatePendingUpload {
		if _, err := db.Exec(ctx, `INSERT INTO media_uploads (object_id, intended_owner_type, intended_owner_id, method,
			max_bytes, expires_at) VALUES ($1, $2, $3, 'POST', 10, $4)`, oid, s.Intended.Type, s.Intended.ID,
			s.ExpiresAt); err != nil {
			return uuid.Nil, err
		}
	}
	for _, v := range s.Variants {
		if _, err := db.Exec(ctx, `INSERT INTO media_variants (object_id, name, blob_key, content_type, width, height,
			bytes) VALUES ($1, $2, $3, 'image/webp', 10, 8, 100)`, oid, v, VariantKey(oid, v)); err != nil {
			return uuid.Nil, err
		}
	}
	return oid, nil
}

// VariantKey — ключ варианта в хранилище: <object_id>/<имя>.webp.
func VariantKey(objectID uuid.UUID, name string) string {
	return objectID.String() + "/" + name + ".webp"
}
