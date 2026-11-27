package blob

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wf/backend/internal/platform/clock"
)

// MinDevSignKeyLen — минимальная длина ключа подписи FS (*_BLOB_DEV_SIGN_KEY).
const MinDevSignKeyLen = 32

// Поля формы подписанной загрузки (R4): как у S3 POST, подпись — своим полем.
const (
	FieldKey         = "key"
	FieldContentType = "Content-Type"
	FieldPolicy      = "policy"
	FieldSignature   = "x-wf-signature"
)

// DevSigner — эмуляция подписи хранилища для FS и Memory (R4): загрузка — подписанный POST на
// <baseURL>/_blob/in с политикой (ключ, тип, точный размер, срок), чтение закрытого —
// <baseURL>/_media/priv/<ключ>?exp=<unix>&sig=<hex>. Подпись — HMAC-SHA256 с меткой назначения
// («upload»/«get»): подпись одного вида не годится для другого.
type DevSigner struct {
	key  []byte
	base string
	clk  clock.Clock
}

var _ Presigner = (*DevSigner)(nil)

// uploadPolicy — политика загрузки; в форме — base64 от JSON.
type uploadPolicy struct {
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
	Exp         int64  `json:"exp"`
}

// NewDevSigner — key не короче 32 байт; baseURL — внешний адрес api (http/https, без запроса).
func NewDevSigner(key []byte, baseURL string, clk clock.Clock) (*DevSigner, error) {
	if len(key) < MinDevSignKeyLen {
		return nil, fmt.Errorf("blob: ключ подписи короче %d байт", MinDevSignKeyLen)
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("blob: адрес подписи %q — нужен абсолютный http(s) без запроса", baseURL)
	}
	if clk == nil {
		return nil, errors.New("blob: нет часов подписи")
	}
	return &DevSigner{key: append([]byte(nil), key...), base: strings.TrimRight(baseURL, "/"), clk: clk}, nil
}

func (s *DevSigner) mac(label string, parts ...string) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(label))
	for _, p := range parts {
		h.Write([]byte{'\n'})
		h.Write([]byte(p))
	}
	return h.Sum(nil)
}

// valid — подпись sig (hex) верна для сообщения; сравнение постоянного времени.
func (s *DevSigner) valid(sig string, label string, parts ...string) bool {
	got, err := hex.DecodeString(sig)
	return err == nil && hmac.Equal(got, s.mac(label, parts...))
}

// expiry — срок в целых секундах: в подписи и ответе одно и то же значение.
func (s *DevSigner) expiry(ttl time.Duration) int64 { return s.clk.Now().Add(ttl).Unix() }

func (s *DevSigner) PresignUpload(_ context.Context, spec UploadSpec) (UploadForm, error) {
	if err := ValidKey(spec.Key); err != nil {
		return UploadForm{}, err
	}
	if spec.ContentType == "" || spec.Bytes <= 0 || spec.TTL <= 0 {
		return UploadForm{}, fmt.Errorf("blob: загрузка без типа, размера или срока")
	}
	p := uploadPolicy{Key: spec.Key, ContentType: spec.ContentType, Bytes: spec.Bytes, Exp: s.expiry(spec.TTL)}
	raw, err := json.Marshal(p)
	if err != nil {
		return UploadForm{}, fmt.Errorf("blob: %w", err)
	}
	policy := base64.StdEncoding.EncodeToString(raw)
	return UploadForm{
		URL: s.base + "/_blob/in",
		Fields: map[string]string{
			FieldKey:         spec.Key,
			FieldContentType: spec.ContentType,
			FieldPolicy:      policy,
			FieldSignature:   hex.EncodeToString(s.mac("upload", policy)),
		},
		ExpiresAt: time.Unix(p.Exp, 0).UTC(),
		MaxBytes:  spec.Bytes,
	}, nil
}

// VerifyUpload проверяет форму загрузки: fields — все поля формы, кроме файла; size — размер файла;
// contentType — поле Content-Type формы (тип объекта, как у S3). Лишнее поле, неверная подпись, другие ключ,
// тип или размер — ErrSignature; срок вышел (now ≥ срока) — ErrExpired. Возвращает подписанный ключ.
func (s *DevSigner) VerifyUpload(fields map[string]string, size int64, contentType string) (string, error) {
	for name := range fields {
		switch name {
		case FieldKey, FieldContentType, FieldPolicy, FieldSignature:
		default:
			return "", ErrSignature
		}
	}
	policy := fields[FieldPolicy]
	if policy == "" || !s.valid(fields[FieldSignature], "upload", policy) {
		return "", ErrSignature
	}
	raw, err := base64.StdEncoding.DecodeString(policy)
	if err != nil {
		return "", ErrSignature
	}
	var p uploadPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", ErrSignature
	}
	if !s.clk.Now().Before(time.Unix(p.Exp, 0)) {
		return "", ErrExpired
	}
	if fields[FieldKey] != p.Key || fields[FieldContentType] != p.ContentType || contentType != p.ContentType ||
		size != p.Bytes {
		return "", ErrSignature
	}
	return p.Key, nil
}

// PresignGet — ссылка на закрытый объект. responseCacheControl в FS не подписывается: заголовок ответа
// ставит обработчик /_media/priv.
func (s *DevSigner) PresignGet(_ context.Context, key string, ttl time.Duration, _ string) (string, time.Time, error) {
	if err := ValidKey(key); err != nil {
		return "", time.Time{}, err
	}
	if ttl <= 0 {
		return "", time.Time{}, fmt.Errorf("blob: ссылка без срока")
	}
	at := s.expiry(ttl)
	exp := strconv.FormatInt(at, 10)
	sig := hex.EncodeToString(s.mac("get", key, exp))
	return s.base + "/_media/priv/" + key + "?exp=" + exp + "&sig=" + sig, time.Unix(at, 0).UTC(), nil
}

// VerifyGet — подпись ссылки на закрытый объект: чужой ключ или exp — ErrSignature, срок вышел — ErrExpired.
func (s *DevSigner) VerifyGet(key, exp, sig string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if !s.valid(sig, "get", key, exp) {
		return ErrSignature
	}
	at, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return ErrSignature
	}
	if !s.clk.Now().Before(time.Unix(at, 0)) {
		return ErrExpired
	}
	return nil
}
