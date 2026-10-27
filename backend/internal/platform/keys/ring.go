package keys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

var (
	// ErrUnknownKey — ключа с таким именем в кольце нет (его убрали из окружения раньше времени).
	ErrUnknownKey = errors.New("keys: ключ не найден в кольце")
	// ErrDecrypt — шифротекст повреждён, обрезан, чужого формата или не от этого aad.
	ErrDecrypt = errors.New("keys: не расшифровывается")
	// ErrNoAAD — Seal или Open без aad: шифротекст не привязан к строке и открылся бы в чужой.
	// Ошибка кода вызывающего, а не данных.
	ErrNoAAD = errors.New("keys: пустой aad — шифротекст не привязан к строке")
)

const (
	minKeyBytes = 32
	sealVersion = 1 // первый байт шифротекста: формат v1 = версия ‖ nonce(12) ‖ AES-GCM(данные) ‖ тег(16)
	// Метки HKDF: из одного материала выводятся независимые ключи шифрования и MAC.
	infoSeal   = "wf/keys/ring/v1/aes-256-gcm"
	infoMACPfx = "wf/keys/ring/v1/hmac-sha256/"
)

// ringKey — ключ кольца: имя (keys.ID материала), AEAD и материал для вывода ключей MAC.
type ringKey struct {
	id       string
	aead     cipher.AEAD
	material []byte
}

// Ring — ключи для ротации (спека бэкенда §6.9): первый — текущий (шифрует и считает MAC новых
// строк), остальные — только расшифровывают и проверяют то, что записано ими раньше. Ключ строки
// (key_id в базе) — имя ключа (keys.ID). Ротация: новый ключ первым, старые — следом, пока
// перешифровка (admin-api staff rekey) не переведёт строки на текущий.
type Ring struct {
	keys []ringKey
	byID map[string]int // имя ключа → индекс в keys
}

// NewRing — кольцо из ключей окружения (keys.Decode каждого, ≥ 32 байт, без повторов). Пустой
// список — ошибка: без ключа сервису нечем шифровать.
func NewRing(encoded []string) (*Ring, error) {
	if len(encoded) == 0 {
		return nil, errors.New("keys: кольцо без ключей")
	}
	r := &Ring{byID: make(map[string]int, len(encoded))}
	for i, s := range encoded {
		material, err := Decode(s)
		if err != nil {
			return nil, fmt.Errorf("keys: ключ %d: %w", i+1, err)
		}
		if len(material) < minKeyBytes {
			return nil, fmt.Errorf("keys: ключ %d: %d байт, нужно ≥ %d", i+1, len(material), minKeyBytes)
		}
		id := ID(material)
		if _, dup := r.byID[id]; dup {
			return nil, fmt.Errorf("keys: ключ %d повторяет более ранний (%s)", i+1, id)
		}
		sealKey, err := hkdf.Key(sha256.New, material, nil, infoSeal, 32)
		if err != nil {
			return nil, err
		}
		block, err := aes.NewCipher(sealKey)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		r.byID[id] = len(r.keys)
		r.keys = append(r.keys, ringKey{id: id, aead: aead, material: material})
	}
	return r, nil
}

// CurrentID — имя текущего ключа: им шифруются и подписываются новые строки.
func (r *Ring) CurrentID() string { return r.keys[0].id }

// Has — есть ли ключ с таким именем (сверка строк перед удалением старого ключа из окружения).
func (r *Ring) Has(keyID string) bool {
	_, ok := r.byID[keyID]
	return ok
}

// Seal — AES-256-GCM текущим ключом. aad — к чему привязан шифротекст (id строки): тот же текст в
// другой строке не расшифруется; пустой aad — ErrNoAAD. ct = версия ‖ nonce ‖ шифротекст ‖ тег;
// keyID — сохранить рядом.
func (r *Ring) Seal(plaintext, aad []byte) (ct []byte, keyID string, err error) {
	if len(aad) == 0 {
		return nil, "", ErrNoAAD
	}
	k := &r.keys[0]
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+k.aead.Overhead())
	out = append(out, sealVersion)
	out = append(out, nonce...)
	return k.aead.Seal(out, nonce, plaintext, aad), k.id, nil
}

// Open — расшифровка ключом keyID: ErrNoAAD — пустой aad; ErrUnknownKey — такого ключа нет;
// ErrDecrypt — повреждено, не тот aad или не тот формат.
func (r *Ring) Open(ct []byte, keyID string, aad []byte) ([]byte, error) {
	if len(aad) == 0 {
		return nil, ErrNoAAD
	}
	i, ok := r.byID[keyID]
	if !ok {
		return nil, ErrUnknownKey
	}
	k := &r.keys[i]
	ns := k.aead.NonceSize()
	if len(ct) < 1+ns+k.aead.Overhead() || ct[0] != sealVersion {
		return nil, ErrDecrypt
	}
	pt, err := k.aead.Open(nil, ct[1:1+ns], ct[1+ns:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// MAC — HMAC-SHA-256 сообщения ключом, выведенным HKDF из ключа keyID и метки label (staff-invite,
// staff-recovery, staff-login-email): у каждой метки свой независимый ключ. Новая строка —
// keyID = CurrentID(), проверка — keyID из строки. Сравнивать — hmac.Equal. Пустая метка — паника
// (ошибка кода); нет ключа — ErrUnknownKey.
func (r *Ring) MAC(keyID, label string, msg []byte) ([]byte, error) {
	if label == "" {
		panic("keys: MAC без метки")
	}
	i, ok := r.byID[keyID]
	if !ok {
		return nil, ErrUnknownKey
	}
	macKey, err := hkdf.Key(sha256.New, r.keys[i].material, nil, infoMACPfx+label, 32)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, macKey)
	m.Write(msg)
	return m.Sum(nil), nil
}
