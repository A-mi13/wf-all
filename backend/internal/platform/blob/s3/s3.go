// Package s3 — S3-совместимое хранилище (спека media §6.2, план R1–R3, R9): бакеты blob.Store поверх
// aws-sdk-go-v2 и подпись blob.Presigner — POST-форма загрузки в in и ссылка GET на priv. Сервер — Garage
// (свой сервер, dev — scripts/s3.sh), но клиент ничего специфичного для Garage не делает.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/clock"
)

// Config — адреса, регион, имена бакетов и ключ процесса.
type Config struct {
	Endpoint, PublicEndpoint, Region string // внутренний адрес; внешний — для подписи
	BucketIn, BucketPub, BucketPriv  string
	AccessKey, SecretKey             string
}

// Open собирает бакеты и подпись; в сеть не ходит. Два клиента (R9): обычный — на Endpoint, подписывающий — на
// PublicEndpoint (SigV4 включает Host: ссылка, подписанная на внутренний адрес, снаружи не пройдёт).
func Open(_ context.Context, c Config, clk clock.Clock) (blob.Buckets, blob.Presigner, error) {
	if err := c.validate(); err != nil {
		return blob.Buckets{}, nil, err
	}
	if clk == nil {
		return blob.Buckets{}, nil, errors.New("blob/s3: нет часов подписи")
	}
	api := newClient(c, c.Endpoint)
	b := blob.Buckets{
		In:   &store{c: api, bucket: c.BucketIn},
		Pub:  &store{c: api, bucket: c.BucketPub},
		Priv: &store{c: api, bucket: c.BucketPriv},
	}
	p := &presigner{
		p:        awss3.NewPresignClient(newClient(c, c.PublicEndpoint)),
		bucketIn: c.BucketIn, bucketPriv: c.BucketPriv,
		clk: clk,
	}
	return b, p, nil
}

func (c Config) validate() error {
	for name, u := range map[string]string{"Endpoint": c.Endpoint, "PublicEndpoint": c.PublicEndpoint} {
		p, err := url.Parse(u)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return fmt.Errorf("blob/s3: %s — не адрес http(s)://хост", name)
		}
	}
	for name, v := range map[string]string{
		"Region": c.Region, "BucketIn": c.BucketIn, "BucketPub": c.BucketPub, "BucketPriv": c.BucketPriv,
		"AccessKey": c.AccessKey, "SecretKey": c.SecretKey,
	} {
		if v == "" {
			return fmt.Errorf("blob/s3: пустой %s", name)
		}
	}
	return nil
}

// newClient — клиент R9: путь /<бакет>/<ключ> (Garage и свой Caddy не держат поддомены бакетов), контрольные
// суммы — только где их требует API (умолчание SDK ломает S3-совместимые серверы на Put/Delete).
func newClient(c Config, endpoint string) *awss3.Client {
	return awss3.New(awss3.Options{
		Region:                     c.Region,
		BaseEndpoint:               aws.String(endpoint),
		UsePathStyle:               true,
		Credentials:                credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
}

// store — один бакет.
type store struct {
	c      *awss3.Client
	bucket string
}

// Put — длина тела нужна подписи (по HTTP SDK хэширует тело и перематывает его): io.ReadSeeker идёт как есть,
// прочее читается в память — размер ограничивает вызывающий (LimitReader по пределу цели).
func (s *store) Put(ctx context.Context, key string, r io.Reader, o blob.PutOptions) error {
	if err := blob.ValidKey(key); err != nil {
		return err
	}
	body, size, err := sized(r)
	if err != nil {
		return fmt.Errorf("blob/s3: put %s/%s: %w", s.bucket, key, err)
	}
	_, err = s.c.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   optional(o.ContentType),
		CacheControl:  optional(o.CacheControl),
	})
	if err != nil {
		return fmt.Errorf("blob/s3: put %s/%s: %w", s.bucket, key, err)
	}
	return nil
}

func sized(r io.Reader) (io.ReadSeeker, int64, error) {
	if rs, ok := r.(io.ReadSeeker); ok {
		cur, err := rs.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, 0, err
		}
		end, err := rs.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, 0, err
		}
		if _, err := rs.Seek(cur, io.SeekStart); err != nil {
			return nil, 0, err
		}
		return rs, end - cur, nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	return bytes.NewReader(b), int64(len(b)), nil
}

func (s *store) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	if err := blob.ValidKey(key); err != nil {
		return nil, blob.Info{}, err
	}
	out, err := s.c.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, blob.Info{}, s.fail("get", key, err)
	}
	return out.Body, blob.Info{
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		CacheControl: aws.ToString(out.CacheControl),
	}, nil
}

func (s *store) Head(ctx context.Context, key string) (blob.Info, error) {
	if err := blob.ValidKey(key); err != nil {
		return blob.Info{}, err
	}
	out, err := s.c.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return blob.Info{}, s.fail("head", key, err)
	}
	return blob.Info{
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		CacheControl: aws.ToString(out.CacheControl),
	}, nil
}

// Delete — S3 отвечает успехом и на отсутствующий ключ: повтор идемпотентен.
func (s *store) Delete(ctx context.Context, key string) error {
	if err := blob.ValidKey(key); err != nil {
		return err
	}
	if _, err := s.c.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("blob/s3: delete %s/%s: %w", s.bucket, key, err)
	}
	return nil
}

func (s *store) List(ctx context.Context, prefix string, fn func(key string) error) error {
	if err := blob.ValidPrefix(prefix); err != nil {
		return err
	}
	pages := awss3.NewListObjectsV2Paginator(s.c, &awss3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: optional(prefix),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("blob/s3: list %s/%s: %w", s.bucket, prefix, err)
		}
		for _, o := range page.Contents {
			if err := fn(aws.ToString(o.Key)); err != nil {
				return err // ошибка колбэка — как есть (контракт Store)
			}
		}
	}
	return nil
}

// fail — нет ключа (GetObject — NoSuchKey, HeadObject без тела — NotFound) → blob.ErrNotFound; прочее (нет бакета,
// нет прав, сеть) — обёрнутая ошибка.
func (s *store) fail(op, key string, err error) error {
	var ae smithy.APIError
	if errors.As(err, &ae) && (ae.ErrorCode() == "NoSuchKey" || ae.ErrorCode() == "NotFound") {
		return blob.ErrNotFound
	}
	return fmt.Errorf("blob/s3: %s %s/%s: %w", op, s.bucket, key, err)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}
