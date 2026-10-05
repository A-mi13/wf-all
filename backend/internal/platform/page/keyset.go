package page

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// versionKeyset — версия wire курсора с произвольным набором полей (v1 — Cursor).
const versionKeyset = 2

// Kind — тип поля сортировки keyset-курсора. Числа констант входят в отпечаток набора:
// порядок не менять, новые — только в конец.
type Kind uint8

const (
	KindInt64  Kind = iota + 1
	KindString      // строка UTF-8 (text из базы)
	KindTime        // timestamptz: микросекунды, как в базе; декодируется в UTC
	KindUUID
)

// Keyset — позиция в списке с произвольным порядком: значения полей сортировки последней
// строки страницы. Set — имя списка с перечнем полей сортировки по порядку
// ("geo.cities(rank,population,id)"): его отпечаток (хэш Set и Kinds) в курсоре отсекает курсор
// чужого списка и старого набора полей (спека geo §4.5 — «хэш имён и типов полей»). Values — по Kinds:
// int64 | string | time.Time | uuid.UUID.
type Keyset struct {
	Set    string
	Values []any
}

// keysetWire — значения строками: int64 в JSON-числе потерял бы точность выше 2^53.
type keysetWire struct {
	V  int      `json:"v"`
	FP string   `json:"f"`
	K  []string `json:"k"`
}

// EncodeKeyset — непрозрачная строка курсора v2 (base64url без заполнения). Пустой Set, нет
// Kinds, неизвестный Kind, число или тип значений не по Kinds — паника: это ошибка
// вызывающего кода, а не данных запроса.
func EncodeKeyset(k Keyset, kinds ...Kind) string {
	mustSpec(k.Set, kinds)
	if len(k.Values) != len(kinds) {
		panic(fmt.Sprintf("page: %s: значений %d, полей %d", k.Set, len(k.Values), len(kinds)))
	}
	vals := make([]string, len(kinds))
	for i, kind := range kinds {
		s, ok := encodeValue(kind, k.Values[i])
		if !ok {
			panic(fmt.Sprintf("page: %s: поле %d: %T не подходит к Kind %d", k.Set, i, k.Values[i], kind))
		}
		vals[i] = s
	}
	b, _ := json.Marshal(keysetWire{V: versionKeyset, FP: fingerprint(k.Set, kinds), K: vals})
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeKeyset — позиция из курсора списка set с полями kinds. Негодная строка, курсор v1,
// курсор другого набора или с другими типами полей — ErrBadCursor (400 validation.failed по
// query.cursor). Пустой set или kinds — паника, как у EncodeKeyset.
func DecodeKeyset(s, set string, kinds ...Kind) (Keyset, error) {
	mustSpec(set, kinds)
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) == 0 {
		return Keyset{}, ErrBadCursor
	}
	var w keysetWire
	if json.Unmarshal(raw, &w) != nil || w.V != versionKeyset || w.FP != fingerprint(set, kinds) ||
		len(w.K) != len(kinds) {
		return Keyset{}, ErrBadCursor
	}
	vals := make([]any, len(kinds))
	for i, kind := range kinds {
		v, ok := decodeValue(kind, w.K[i])
		if !ok {
			return Keyset{}, ErrBadCursor
		}
		vals[i] = v
	}
	return Keyset{Set: set, Values: vals}, nil
}

func mustSpec(set string, kinds []Kind) {
	if set == "" {
		panic("page: keyset без имени набора")
	}
	if len(kinds) == 0 {
		panic("page: keyset " + set + " без полей")
	}
	for i, k := range kinds {
		if k < KindInt64 || k > KindUUID {
			panic(fmt.Sprintf("page: %s: поле %d: неизвестный Kind %d", set, i, k))
		}
	}
}

// fingerprint — отпечаток набора: не секрет и не подпись (собранный руками курсор лишь
// сдвинет страницу самому клиенту), а защита от курсора чужого списка или старого набора полей.
func fingerprint(set string, kinds []Kind) string {
	b := make([]byte, 0, len(set)+1+len(kinds))
	b = append(b, set...)
	b = append(b, 0)
	for _, k := range kinds {
		b = append(b, byte(k))
	}
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func encodeValue(kind Kind, v any) (string, bool) {
	switch kind {
	case KindInt64:
		n, ok := v.(int64)
		return strconv.FormatInt(n, 10), ok
	case KindString:
		s, ok := v.(string)
		return s, ok
	case KindTime:
		t, ok := v.(time.Time)
		return strconv.FormatInt(t.UnixMicro(), 10), ok
	case KindUUID:
		u, ok := v.(uuid.UUID)
		return u.String(), ok
	}
	return "", false
}

func decodeValue(kind Kind, s string) (any, bool) {
	switch kind {
	case KindInt64:
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	case KindString:
		return s, true
	case KindTime:
		n, err := strconv.ParseInt(s, 10, 64)
		return time.UnixMicro(n).UTC(), err == nil
	case KindUUID:
		u, err := uuid.Parse(s)
		return u, err == nil
	}
	return nil, false
}
