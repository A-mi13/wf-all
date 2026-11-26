package media

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/media/internal/domain"
	"wf/backend/internal/media/internal/store/mediadb"
)

// Image — объект из media_read_objects (§5.4); нет строки — (Image{}, false, nil): ссылка владельца на удалённый
// объект — «изображения нет», не ошибка (§2.4).
func (s *service) Image(ctx context.Context, objectID uuid.UUID) (Image, bool, error) {
	row, err := s.q.ReadObject(ctx, objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Image{}, false, nil
	}
	if err != nil {
		return Image{}, false, fmt.Errorf("media: объект: %w", err)
	}
	img, err := s.imageOf(row)
	return img, err == nil, err
}

// Images — пачкой; отсутствующих id в карте нет.
func (s *service) Images(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Image, error) {
	out := make(map[uuid.UUID]Image, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ReadObjects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("media: объекты: %w", err)
	}
	for _, row := range rows {
		img, err := s.imageOf(row)
		if err != nil {
			return nil, err
		}
		out[row.ID] = img
	}
	return out, nil
}

// imageOf — схема MediaImage (§7.1): варианты только у ready, адрес — только у публичного (§3.5).
func (s *service) imageOf(row mediadb.MediaReadObject) (Image, error) {
	state := State(row.State)
	img := Image{ID: row.ID, State: domain.ClientState(state)}
	if row.RejectCode != nil {
		img.RejectCode = RejectCode(*row.RejectCode)
	}
	if !domain.ShowVariants(state) {
		return img, nil
	}
	vs, err := domain.ParseVariants(row.Variants)
	if err != nil {
		return Image{}, err
	}
	img.Variants = make(map[string]VariantView, len(vs))
	for _, v := range vs {
		img.Variants[v.Name] = VariantView{URL: domain.VariantURL(row.Visibility, s.d.Blob.PublicBaseURL, v.BlobKey),
			ContentType: v.ContentType, Width: deref(v.Width), Height: deref(v.Height)}
	}
	return img, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// PrivateURL — подписанный GET на вариант закрытого ready-объекта (§3.5): 5 минут, ответ private, no-store. Публичный
// объект, не ready, нет варианта или строки — ErrObjectUnavailable (публичного адреса у закрытого нет, а подписанной
// ссылки у публичного — тоже: страж «закрытое», §12).
func (s *service) PrivateURL(ctx context.Context, objectID uuid.UUID, variant string) (PrivateLink, error) {
	if !s.Available() {
		return PrivateLink{}, ErrUnavailable
	}
	row, err := s.q.ReadObject(ctx, objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PrivateLink{}, ErrObjectUnavailable
	}
	if err != nil {
		return PrivateLink{}, fmt.Errorf("media: объект: %w", err)
	}
	if Visibility(row.Visibility) != VisibilityPrivate || State(row.State) != StateReady {
		return PrivateLink{}, ErrObjectUnavailable
	}
	vs, err := domain.ParseVariants(row.Variants)
	if err != nil {
		return PrivateLink{}, err
	}
	for _, v := range vs {
		if v.Name != variant {
			continue
		}
		u, exp, err := s.d.Blob.Presigner.PresignGet(ctx, v.BlobKey, privateLinkTTL, privateCacheControl)
		if err != nil {
			return PrivateLink{}, fmt.Errorf("media: подпись ссылки: %w", err)
		}
		return PrivateLink{URL: u, ExpiresAt: exp}, nil
	}
	return PrivateLink{}, ErrObjectUnavailable
}
