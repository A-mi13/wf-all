package media

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/blob"
)

// ConfirmUpload — проверка перед транзакцией владельца «завершить» (§3.1 п. 5; сеть — вне транзакции). Билета нет
// (объект прикреплён или строки нет) — nil без HEAD: повтор идемпотентен (m1), годность решит AttachInTx. Билет на
// другого владельца или от другого загрузившего — так же nil без HEAD (ревью Task 6 m5): чужой неотличим от
// несуществующего, отказ object_unavailable даст AttachInTx. Окно — строго до expires_at + 1 ч (m2), позже —
// ErrUploadExpired. Файла во входящем нет — ErrUploadIncomplete.
func (s *service) ConfirmUpload(ctx context.Context, uploadID uuid.UUID, owner OwnerRef, by Uploader) error {
	if !s.Available() {
		return ErrUnavailable
	}
	u, err := s.q.GetUploadWithUploader(ctx, uploadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("media: билет: %w", err)
	}
	uploader := Uploader{UserID: derefID(u.UploadedByUser), StaffID: derefID(u.UploadedByStaff)}
	if u.IntendedOwnerType != owner.Type || u.IntendedOwnerID != owner.ID || uploader != by {
		return nil
	}
	if !s.d.Clock.Now().Before(u.ExpiresAt.Add(CompleteWindow)) {
		return ErrUploadExpired
	}
	if _, err := s.d.Blob.Buckets.In.Head(ctx, uploadID.String()); errors.Is(err, blob.ErrNotFound) {
		return ErrUploadIncomplete
	} else if err != nil {
		return fmt.Errorf("media: HEAD входящего: %w", err)
	}
	return nil
}

// derefID — uuid колонки (NULL — uuid.Nil).
func derefID(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}
