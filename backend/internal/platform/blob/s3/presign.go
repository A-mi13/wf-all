package s3

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/clock"
)

// presigner — подпись на клиенте с PublicEndpoint. Время подписи — от clk: SDK берёт свои часы (sdk.NowTime),
// поэтому штатные подписчики SDK оборачиваются и получают время clk.
type presigner struct {
	p                    *awss3.PresignClient
	bucketIn, bucketPriv string
	clk                  clock.Clock
}

// PresignUpload — POST-форма (R3): политика с content-length-range [Bytes, Bytes], eq $Content-Type и ключом ровно
// Key (условие key SDK добавляет сам). Поле Content-Type SDK в форму не кладёт — без него сервер отвергнет честную
// загрузку («is required in policy»), поэтому оно добавляется здесь.
func (p *presigner) PresignUpload(ctx context.Context, s blob.UploadSpec) (blob.UploadForm, error) {
	if err := blob.ValidKey(s.Key); err != nil {
		return blob.UploadForm{}, err
	}
	if s.ContentType == "" || s.Bytes <= 0 || s.TTL <= 0 {
		return blob.UploadForm{}, errors.New("blob/s3: загрузка без типа, размера или срока")
	}
	now := p.clk.Now().UTC()
	// expiration политики — RFC 3339 без долей секунды: тот же срок отдаётся наружу
	exp := now.Add(s.TTL).Truncate(time.Second)
	req, err := p.p.PresignPostObject(ctx,
		&awss3.PutObjectInput{Bucket: aws.String(p.bucketIn), Key: aws.String(s.Key)},
		func(o *awss3.PresignPostOptions) {
			o.Expires = s.TTL
			o.Conditions = []any{
				[]any{"content-length-range", s.Bytes, s.Bytes},
				[]any{"eq", "$" + blob.FieldContentType, s.ContentType},
			}
			o.PostPresigner = clockedPost{inner: o.PostPresigner, now: now, exp: exp}
		})
	if err != nil {
		return blob.UploadForm{}, fmt.Errorf("blob/s3: подпись загрузки: %w", err)
	}
	fields := make(map[string]string, len(req.Values)+1)
	for k, v := range req.Values {
		fields[k] = v
	}
	fields[blob.FieldContentType] = s.ContentType
	return blob.UploadForm{URL: req.URL, Fields: fields, ExpiresAt: exp, MaxBytes: s.Bytes}, nil
}

// PresignGet — ссылка GET на priv; responseCacheControl подписывается и становится заголовком ответа.
// Срок — целые секунды (X-Amz-Expires), отсчёт — от X-Amz-Date (секунды без долей).
func (p *presigner) PresignGet(ctx context.Context, key string, ttl time.Duration, responseCacheControl string) (string, time.Time, error) {
	if err := blob.ValidKey(key); err != nil {
		return "", time.Time{}, err
	}
	ttl = ttl.Truncate(time.Second)
	if ttl <= 0 {
		return "", time.Time{}, errors.New("blob/s3: ссылка без срока")
	}
	now := p.clk.Now().UTC().Truncate(time.Second)
	req, err := p.p.PresignGetObject(ctx,
		&awss3.GetObjectInput{
			Bucket:               aws.String(p.bucketPriv),
			Key:                  aws.String(key),
			ResponseCacheControl: optional(responseCacheControl),
		},
		func(o *awss3.PresignOptions) {
			o.Expires = ttl
			o.Presigner = clockedGet{inner: o.Presigner, now: now}
		})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("blob/s3: подпись ссылки: %w", err)
	}
	return req.URL, now.Add(ttl), nil
}

// clockedPost — подпись POST-политики штатным подписчиком SDK со временем и сроком от clk.
type clockedPost struct {
	inner    awss3.PresignPost
	now, exp time.Time
}

func (c clockedPost) PresignPost(creds aws.Credentials, bucket, key, region, service string, _ time.Time,
	conditions []any, _ time.Time, optFns ...func(*v4.SignerOptions),
) (map[string]string, error) {
	return c.inner.PresignPost(creds, bucket, key, region, service, c.now, conditions, c.exp, optFns...)
}

// clockedGet — подпись ссылки штатным подписчиком SDK (у S3 — без экранирования пути) со временем от clk.
type clockedGet struct {
	inner awss3.HTTPPresignerV4
	now   time.Time
}

func (c clockedGet) PresignHTTP(ctx context.Context, creds aws.Credentials, r *http.Request, payloadHash, service,
	region string, _ time.Time, optFns ...func(*v4.SignerOptions),
) (string, http.Header, error) {
	return c.inner.PresignHTTP(ctx, creds, r, payloadHash, service, region, c.now, optFns...)
}
