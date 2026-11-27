package blobtest

import (
	"os"
	"testing"
)

// Бакеты dev-Garage (scripts/s3/init.sh, итоги спайка R1).
const (
	S3BucketIn   = "wf-in"
	S3BucketPub  = "wf-pub"
	S3BucketPriv = "wf-priv"
)

// S3Key — ключ доступа процесса.
type S3Key struct{ AccessKey, SecretKey string }

// S3 — dev-Garage из `bash scripts/s3.sh up`: адреса, регион и ключи процессов с правами по R2.
type S3 struct {
	// Endpoint — внутренний адрес; PublicEndpoint — тот же сервер под другим Host (подписанные ссылки, R9).
	Endpoint, PublicEndpoint, Region string
	API, Admin, Worker               S3Key
}

// S3Env — переменные WF_TEST_S3_* (`eval "$(bash scripts/s3.sh env)"`). Без WF_TEST_S3_ENDPOINT тест
// пропускается, а при CI=true падает (R13): в CI шаг «Garage для тестов» обязан их выставить.
func S3Env(t testing.TB) S3 {
	t.Helper()
	ep := os.Getenv("WF_TEST_S3_ENDPOINT")
	if ep == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("CI=true, но WF_TEST_S3_ENDPOINT пуст: шаг «Garage для тестов» не выставил переменные")
		}
		t.Skip("нет WF_TEST_S3_ENDPOINT — bash scripts/s3.sh up && eval \"$(bash scripts/s3.sh env)\"")
	}
	s := S3{
		Endpoint:       ep,
		PublicEndpoint: os.Getenv("WF_TEST_S3_PUBLIC_ENDPOINT"),
		Region:         os.Getenv("WF_TEST_S3_REGION"),
		API:            s3Key("API"),
		Admin:          s3Key("ADMIN"),
		Worker:         s3Key("WORKER"),
	}
	for name, v := range map[string]string{
		"WF_TEST_S3_PUBLIC_ENDPOINT": s.PublicEndpoint, "WF_TEST_S3_REGION": s.Region,
		"WF_TEST_S3_API_*": s.API.SecretKey, "WF_TEST_S3_ADMIN_*": s.Admin.SecretKey,
		"WF_TEST_S3_WORKER_*": s.Worker.SecretKey,
	} {
		if v == "" {
			t.Fatalf("WF_TEST_S3_ENDPOINT задан, а %s пуст — eval \"$(bash scripts/s3.sh env)\"", name)
		}
	}
	return s
}

func s3Key(role string) S3Key {
	k := S3Key{
		AccessKey: os.Getenv("WF_TEST_S3_" + role + "_ACCESS_KEY"),
		SecretKey: os.Getenv("WF_TEST_S3_" + role + "_SECRET_KEY"),
	}
	if k.AccessKey == "" {
		k.SecretKey = "" // неполный ключ — как отсутствующий
	}
	return k
}
