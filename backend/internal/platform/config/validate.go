package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/caarlos0/env/v11"

	"wf/backend/internal/platform/keys"
)

// Проверки WORKER_* до подключения к базе — одни и те же у воркера (отдельного и встроенного в api, workerhost) и у
// стража стенда (render.yaml): значение, которое не пустит процесс, краснит тест, а не деплой (ревью infra m4).

// Validate — relay outbox: пачка меньше 1 зациклила бы Drain, неположительный опрос уронил бы time.NewTicker паникой.
func (r Relay) Validate() error {
	var errs []error
	if r.Batch < 1 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_BATCH = %d: нужно не меньше 1", r.Batch))
	}
	if r.Poll <= 0 {
		errs = append(errs, fmt.Errorf("WORKER_RELAY_POLL = %s: нужно больше нуля", r.Poll))
	}
	return errors.Join(errs...)
}

// ValidateQueues — конкурентность каждой обслуживаемой очереди ≥ 1 (R4): River отверг бы MaxWorkers < 1 в NewClient
// уже после подключения. Очередь import проверяется, только если импорт включён (GeoImportEnabled).
func (w Worker) ValidateQueues() error {
	q := w.Queues
	type check struct {
		key string
		n   int
	}
	checks := []check{
		{"WORKER_QUEUE_EVENTS", q.Events},
		{"WORKER_QUEUE_LIFECYCLE", q.Lifecycle},
		{"WORKER_QUEUE_NOTIFY", q.Notify},
		{"WORKER_QUEUE_MAIL", q.Mail},
		{"WORKER_QUEUE_STATS", q.Stats},
		{"WORKER_QUEUE_MEDIA", q.Media},
		{"WORKER_QUEUE_MAINTENANCE", q.Maintenance},
	}
	if w.GeoImportEnabled {
		checks = append(checks, check{"WORKER_QUEUE_IMPORT", q.Import})
	}
	var errs []error
	for _, c := range checks {
		if c.n < 1 {
			errs = append(errs, fmt.Errorf("%s = %d: нужно не меньше 1", c.key, c.n))
		}
	}
	return errors.Join(errs...)
}

// Признак прод-образа (R15): backend/Dockerfile задаёт его в финальной стадии, LoadBlob читает из окружения процесса.
const (
	BuildKindVar  = "WF_BUILD_KIND"
	BuildKindProd = "prod"
)

// LoadBlob — ключи хранилища процесса с префиксом prefix и их проверка до старта (Validate с признаком прод-образа из
// того же окружения).
func LoadBlob(prefix string, environ []string) (Blob, error) {
	b, err := Load[Blob](prefix, environ)
	if err != nil {
		return Blob{}, err
	}
	if err := b.Validate(prefix, env.ToMap(environ)[BuildKindVar]); err != nil {
		return Blob{}, err
	}
	return b, nil
}

// devSignKeyMin — как blob.MinDevSignKeyLen (config не тянет пакет хранилища).
const devSignKeyMin = 32

// Validate — ключи режима (спека media §6.4, R15, R17); в ошибке — полное имя ключа (prefix + имя), без значений
// секретов. none — прочие ключи не нужны и не проверяются; fs — корень, адрес api, ключ подписи, не в прод-образе;
// s3 — все S3_* и адрес публичного медиа, адреса — http(s)://хост (у адреса медиа можно путь).
func (b Blob) Validate(prefix, buildKind string) error {
	backend := prefix + "BLOB_BACKEND"
	var errs []error
	need := func(name, v string) {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s%s: обязателен при %s=%s", prefix, name, backend, b.Backend))
		}
	}
	switch b.Backend {
	case "none":
		return nil
	case "fs":
		if buildKind == BuildKindProd {
			return fmt.Errorf("%s=fs: в прод-образе (%s=%s) файловое хранилище запрещено — s3 или none", backend,
				BuildKindVar, BuildKindProd)
		}
		need("BLOB_FS_ROOT", b.FSRoot)
		need("BLOB_FS_BASE_URL", b.FSBaseURL)
		need("BLOB_DEV_SIGN_KEY", b.DevSignKey)
		if b.FSBaseURL != "" && !rootURL(b.FSBaseURL) {
			errs = append(errs, fmt.Errorf("%sBLOB_FS_BASE_URL: нужен внешний адрес api http(s)://хост[:порт] без пути и запроса",
				prefix))
		}
		if b.DevSignKey != "" {
			if k, err := keys.Decode(b.DevSignKey); err != nil || len(k) < devSignKeyMin {
				errs = append(errs, fmt.Errorf("%sBLOB_DEV_SIGN_KEY: нужен base64 не короче %d байт", prefix, devSignKeyMin))
			}
		}
		if b.MediaPublicBaseURL != "" && b.FSBaseURL != "" && withSlash(b.MediaPublicBaseURL) != b.PublicBaseURL() {
			errs = append(errs, fmt.Errorf("%sMEDIA_PUBLIC_BASE_URL: при fs — <%sBLOB_FS_BASE_URL>/_media/pub/ или пусто",
				prefix, prefix))
		}
	case "s3":
		need("BLOB_S3_ENDPOINT", b.S3Endpoint)
		need("BLOB_S3_PUBLIC_ENDPOINT", b.S3PublicEndpoint)
		need("BLOB_S3_REGION", b.S3Region)
		need("BLOB_S3_BUCKET_IN", b.S3BucketIn)
		need("BLOB_S3_BUCKET_PUB", b.S3BucketPub)
		need("BLOB_S3_BUCKET_PRIV", b.S3BucketPriv)
		need("BLOB_S3_ACCESS_KEY", b.S3AccessKey)
		need("BLOB_S3_SECRET_KEY", b.S3SecretKey)
		need("MEDIA_PUBLIC_BASE_URL", b.MediaPublicBaseURL)
		// формат адресов — здесь, с именем ключа процесса (у трёх сервисов имена полей одинаковые), а не только в
		// blob/s3 при открытии
		for name, v := range map[string]string{"BLOB_S3_ENDPOINT": b.S3Endpoint,
			"BLOB_S3_PUBLIC_ENDPOINT": b.S3PublicEndpoint} {
			if v != "" && !rootURL(v) {
				errs = append(errs, fmt.Errorf("%s%s: нужен адрес http(s)://хост[:порт] без пути, запроса и пользователя",
					prefix, name))
			}
		}
		if v := b.MediaPublicBaseURL; v != "" && !baseURL(v) {
			errs = append(errs, fmt.Errorf("%sMEDIA_PUBLIC_BASE_URL: нужен адрес http(s)://хост[:порт]/[путь/] без "+
				"запроса, фрагмента и пользователя", prefix))
		}
	default:
		return fmt.Errorf("%s = %q: допустимо fs, s3 или none", backend, b.Backend)
	}
	return errors.Join(errs...)
}

// PublicBaseURL — адрес публичных вариантов с завершающим «/»: fs — <FS_BASE_URL>/_media/pub/ (R17), s3 —
// MEDIA_PUBLIC_BASE_URL, none — пусто.
func (b Blob) PublicBaseURL() string {
	switch b.Backend {
	case "fs":
		return strings.TrimSuffix(b.FSBaseURL, "/") + "/_media/pub/"
	case "s3":
		return withSlash(b.MediaPublicBaseURL)
	}
	return ""
}

func withSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s
	}
	return s + "/"
}

// rootURL — абсолютный http(s)-адрес без пользователя, пути (кроме «/»), запроса и фрагмента.
func rootURL(s string) bool {
	u, ok := baseURLParts(s)
	return ok && (u.Path == "" || u.Path == "/")
}

// baseURL — абсолютный http(s)-адрес с любым путём, без пользователя, запроса и фрагмента (к нему дописывается ключ).
func baseURL(s string) bool {
	_, ok := baseURLParts(s)
	return ok
}

func baseURLParts(s string) (*url.URL, bool) {
	u, err := url.Parse(s)
	ok := err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.HasSuffix(s, "#")
	return u, ok
}
