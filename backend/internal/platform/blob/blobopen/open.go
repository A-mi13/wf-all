// Package blobopen — фабрика хранилища процесса по конфигурации (спека media §6.4): fs, s3 или none. Отдельный
// пакет, а не blob.Open: blob/s3 импортирует blob, обратный импорт дал бы цикл.
package blobopen

import (
	"context"
	"fmt"
	"net/url"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/s3"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/keys"
)

// Open открывает хранилище по ключам процесса; в сеть не ходит. Ключи — из config.LoadBlob (разбор и проверка с
// признаком прод-образа: fs в прод-образе отвергается там); здесь Validate повторяется без признака — негодная
// конфигурация не даёт полуоткрытого хранилища. none — Available=false без ошибки. Сброс кэша CDN — всегда
// NoopPurger (R10).
func Open(ctx context.Context, c config.Blob, clk clock.Clock) (blob.Opened, error) {
	if err := c.Validate("", ""); err != nil {
		return blob.Opened{}, fmt.Errorf("blobopen: %w", err)
	}
	o := blob.Opened{Purger: blob.NoopPurger{}}
	switch c.Backend {
	case "none":
		return o, nil
	case "fs":
		key, err := keys.Decode(c.DevSignKey)
		if err != nil {
			return blob.Opened{}, fmt.Errorf("blobopen: BLOB_DEV_SIGN_KEY: %w", err)
		}
		signer, err := blob.NewDevSigner(key, c.FSBaseURL, clk)
		if err != nil {
			return blob.Opened{}, err
		}
		b, err := blob.NewFSBuckets(c.FSRoot)
		if err != nil {
			return blob.Opened{}, err
		}
		o.Buckets, o.Presigner, o.Dev = b, signer, signer
	case "s3":
		if u, err := url.Parse(c.PublicBaseURL()); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return blob.Opened{}, fmt.Errorf("blobopen: MEDIA_PUBLIC_BASE_URL — нужен адрес http(s)://хост/")
		}
		b, p, err := s3.Open(ctx, s3.Config{
			Endpoint: c.S3Endpoint, PublicEndpoint: c.S3PublicEndpoint, Region: c.S3Region,
			BucketIn: c.S3BucketIn, BucketPub: c.S3BucketPub, BucketPriv: c.S3BucketPriv,
			AccessKey: c.S3AccessKey, SecretKey: c.S3SecretKey,
		}, clk)
		if err != nil {
			return blob.Opened{}, err
		}
		o.Buckets, o.Presigner = b, p
	}
	o.PublicBaseURL = c.PublicBaseURL()
	o.Available = true
	return o, nil
}
