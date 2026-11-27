// Package subscribers — подписчики media на события других модулей (спека media §3.10). Регистрирует их воркер:
// cmd/internal/workerhost.Subscriptions.
package subscribers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/platform/events"
)

// SubscriberUploaders — подписчик удаления аккаунта (имя — ключ event_inbox и --subscriber переигровки; не менять).
const SubscriberUploaders = "media.uploaders"

var errPayload = errors.New("media/subscribers: identity.user_deleted без user_id")

// Deps — зависимости подписчиков: журнал воркера (итог — только числа).
type Deps struct {
	Log *slog.Logger
}

// Subscriptions — подписки media: удаление аккаунта (identity.user_deleted, каждое событие).
func Subscriptions(d Deps) []events.Subscription {
	return []events.Subscription{{Subscriber: SubscriberUploaders, EventType: identity.EventUserDeleted,
		Delivery: events.EveryEvent, Handle: userDeleted(d.Log)}}
}

// userDeleted — I-6, §3.10: в транзакции доставки сначала удаляются незавершённые билеты аккаунта (pending_upload;
// входящее без строки уберут правило жизненного цикла и чистка), затем у остальных его объектов обнуляется
// uploaded_by_user (фото полей остаются — они о поле). Повтор безвреден: второй проход ничего не находит.
func userDeleted(log *slog.Logger) events.Handler {
	return func(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
		var p identity.UserPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("media/subscribers: identity.user_deleted: %w", err)
		}
		if p.UserID == uuid.Nil {
			return errPayload
		}
		q := mediadb.New(tx)
		pending, err := q.DeletePendingByUser(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("media/subscribers: незавершённые билеты аккаунта: %w", err)
		}
		cleared, err := q.ClearUploadedByUser(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("media/subscribers: обезличивание загрузившего: %w", err)
		}
		if log != nil && (len(pending) > 0 || cleared > 0) {
			log.InfoContext(ctx, "media: аккаунт удалён", "event_id", e.ID, "pending_deleted", len(pending),
				"uploader_cleared", cleared)
		}
		return nil
	}
}
