package domain

import (
	"encoding/json"
	"fmt"
)

// ClientState — состояние для владельца (схема MediaImage, §7.1): uploaded — ещё «обрабатывается» (processing);
// прочие — как есть (detached, taken_down, pending_upload владелец показывает как «изображения нет»).
func ClientState(s State) State {
	if s == StateUploaded {
		return StateProcessing
	}
	return s
}

// StoredVariant — вариант из media_read_objects.variants (jsonb-массив представления 0028).
type StoredVariant struct {
	Name        string `json:"name"`
	BlobKey     string `json:"blob_key"`
	ContentType string `json:"content_type"`
	Width       *int   `json:"width"`
	Height      *int   `json:"height"`
}

// ParseVariants — массив вариантов представления; пустой raw — нет вариантов.
func ParseVariants(raw []byte) ([]StoredVariant, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var vs []StoredVariant
	if err := json.Unmarshal(raw, &vs); err != nil {
		return nil, fmt.Errorf("media: варианты представления: %w", err)
	}
	return vs, nil
}

// ShowVariants — варианты видны владельцу только у ready (§7.1: у processing и rejected пусто; у снятого и
// откреплённого файлы удаляются).
func ShowVariants(s State) bool { return s == StateReady }

// VariantURL — адрес варианта в ответе владельца: у публичного — публичный адрес, у закрытого — пусто (только
// подписанная ссылка PrivateURL, §3.5: публичного адреса у закрытого нет ни на каком шаге).
func VariantURL(visibility, publicBase, key string) string {
	if visibility != "public" {
		return ""
	}
	return publicBase + key
}
