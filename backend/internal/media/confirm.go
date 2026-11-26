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
// (объект прикреплён или строки нет) — nil без HEAD: повтор идемпотентен (m1), годность решит AttachInTx. Окно —
// строго до expires_at + 1 ч (m2), позже — ErrUploadExpired. Файла во входящем нет — ErrUploadIncomplete.
func (s *service) ConfirmUpload(ctx context.Context, uploadID uuid.UUID) error {
	if !s.Available() {
		return ErrUnavailable
	}
	u, err := s.q.GetUpload(ctx, uploadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("media: билет: %w", err)
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
