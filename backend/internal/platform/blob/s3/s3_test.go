package s3_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/blob/s3"
	"wf/backend/internal/platform/testkit/clocktest"
)

var t0 = time.Date(2026, 11, 26, 12, 30, 15, 0, time.UTC)

func testConfig() s3.Config {
	return s3.Config{
		Endpoint:       "http://garage:3900",
		PublicEndpoint: "https://files.wf.test",
		Region:         "garage",
		BucketIn:       "wf-in",
		BucketPub:      "wf-pub",
		BucketPriv:     "wf-priv",
		AccessKey:      "GKtest",
		SecretKey:      "secret",
	}
}

func presigner(t *testing.T) blob.Presigner {
	t.Helper()
	_, p, err := s3.Open(context.Background(), testConfig(), clocktest.New(t0))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPresignUploadPolicy(t *testing.T) {
	spec := blob.UploadSpec{Key: "0193a1b2/orig", ContentType: "image/jpeg", Bytes: 12345, TTL: 15 * time.Minute} // gitleaks:allow — ключ объекта хранилища, не секрет
	f, err := presigner(t).PresignUpload(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "files.wf.test" || u.Path != "/wf-in" {
		t.Fatalf("URL = %q, ждали адрес PublicEndpoint и бакет wf-in", f.URL)
	}
	if want := t0.Add(spec.TTL); !f.ExpiresAt.Equal(want) || f.MaxBytes != spec.Bytes {
		t.Fatalf("ExpiresAt %v MaxBytes %d, ждали %v %d", f.ExpiresAt, f.MaxBytes, want, spec.Bytes)
	}

	var names []string
	lower := map[string]string{}
	for k, v := range f.Fields {
		names = append(names, strings.ToLower(k))
		lower[strings.ToLower(k)] = v
	}
	slices.Sort(names)
	want := []string{"content-type", "key", "policy", "x-amz-algorithm", "x-amz-credential", "x-amz-date", "x-amz-signature"}
	if !slices.Equal(names, want) {
		t.Fatalf("поля формы %q, ждали %q", names, want)
	}
	if lower["key"] != spec.Key || lower["content-type"] != spec.ContentType ||
		lower["x-amz-date"] != "20261126T123015Z" || !strings.HasPrefix(lower["x-amz-credential"], "GKtest/20261126/garage/s3/") {
		t.Fatalf("поля формы: %v", f.Fields)
	}

	raw, err := base64.StdEncoding.DecodeString(lower["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Expiration string `json:"expiration"`
		Conditions []any  `json:"conditions"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Expiration != "2026-11-26T12:45:15Z" {
		t.Fatalf("expiration %q — не clock + TTL", policy.Expiration)
	}
	var lengthRange, typeEq, keyEq, bucketEq bool
	for _, c := range policy.Conditions {
		switch c := c.(type) {
		case []any:
			if len(c) == 3 && c[0] == "content-length-range" && c[1] == float64(spec.Bytes) && c[2] == float64(spec.Bytes) {
				lengthRange = true
			}
			if len(c) == 3 && c[0] == "eq" && c[1] == "$Content-Type" && c[2] == spec.ContentType {
				typeEq = true
			}
		case map[string]any:
			keyEq = keyEq || c["key"] == spec.Key
			bucketEq = bucketEq || c["bucket"] == "wf-in"
		}
	}
	if !lengthRange || !typeEq || !keyEq || !bucketEq {
		t.Fatalf("условия политики %s: content-length-range [N,N] %v, eq $Content-Type %v, key %v, bucket %v",
			raw, lengthRange, typeEq, keyEq, bucketEq)
	}
}

func TestPresignUploadRejects(t *testing.T) {
	p := presigner(t)
	for name, s := range map[string]blob.UploadSpec{
		"ключ":   {Key: "../x", ContentType: "image/jpeg", Bytes: 1, TTL: time.Minute},
		"тип":    {Key: "a", Bytes: 1, TTL: time.Minute},
		"размер": {Key: "a", ContentType: "image/jpeg", TTL: time.Minute},
		"срок":   {Key: "a", ContentType: "image/jpeg", Bytes: 1},
	} {
		if _, err := p.PresignUpload(context.Background(), s); err == nil {
			t.Errorf("%s: подписано", name)
		}
	}
}

func TestPresignGetHost(t *testing.T) {
	p := presigner(t)
	raw, exp, err := p.PresignGet(context.Background(), "0193a1b2/full.webp", 2*time.Minute, "private, max-age=120")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "files.wf.test" || u.Path != "/wf-priv/0193a1b2/full.webp" {
		t.Fatalf("URL %q — не PublicEndpoint или не бакет wf-priv", raw)
	}
	if q.Get("response-cache-control") != "private, max-age=120" {
		t.Fatalf("response-cache-control = %q", q.Get("response-cache-control"))
	}
	if q.Get("X-Amz-Date") != "20261126T123015Z" || q.Get("X-Amz-Expires") != "120" || q.Get("X-Amz-Signature") == "" {
		t.Fatalf("подпись не от часов clock: %v", q)
	}
	if !exp.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("expiresAt %v", exp)
	}
	if _, _, err := p.PresignGet(context.Background(), "a/../b", time.Minute, ""); err == nil {
		t.Fatal("недопустимый ключ подписан")
	}
	if _, _, err := p.PresignGet(context.Background(), "a", 0, ""); err == nil {
		t.Fatal("ссылка без срока подписана")
	}
}

func TestOpenRejectsIncompleteConfig(t *testing.T) {
	clk := clocktest.New(t0)
	breaks := map[string]func(*s3.Config){
		"Endpoint":       func(c *s3.Config) { c.Endpoint = "" },
		"PublicEndpoint": func(c *s3.Config) { c.PublicEndpoint = "files.wf.test" },
		"Region":         func(c *s3.Config) { c.Region = "" },
		"BucketIn":       func(c *s3.Config) { c.BucketIn = "" },
		"BucketPub":      func(c *s3.Config) { c.BucketPub = "" },
		"BucketPriv":     func(c *s3.Config) { c.BucketPriv = "" },
		"AccessKey":      func(c *s3.Config) { c.AccessKey = "" },
		"SecretKey":      func(c *s3.Config) { c.SecretKey = "" },
	}
	for name, br := range breaks {
		c := testConfig()
		br(&c)
		if _, _, err := s3.Open(context.Background(), c, clk); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := s3.Open(context.Background(), testConfig(), nil); err == nil {
		t.Error("без часов: открыто")
	}
}

// TestS3RequiresEndpointInCI — R13: без WF_TEST_S3_ENDPOINT интеграционный тест пропускается, а при CI=true
// падает (blobtest.S3Env). Проверяется на настоящем тесте пакета в отдельном процессе.
func TestS3RequiresEndpointInCI(t *testing.T) {
	run := func(ci bool) (string, error) {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestS3StoreContract$", "-test.v", "-test.count=1") //nolint:gosec // G204: свой же тестовый бинарник
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "WF_TEST_S3_") && !strings.HasPrefix(kv, "CI=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		if ci {
			cmd.Env = append(cmd.Env, "CI=true")
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run(true)
	if err == nil || !strings.Contains(out, "WF_TEST_S3_ENDPOINT") || !strings.Contains(out, "--- FAIL: TestS3StoreContract") {
		t.Fatalf("CI=true без WF_TEST_S3_ENDPOINT: ждали падение, err=%v\n%s", err, out)
	}
	out, err = run(false)
	if err != nil || !strings.Contains(out, "--- SKIP: TestS3StoreContract") {
		t.Fatalf("без CI и WF_TEST_S3_ENDPOINT: ждали пропуск, err=%v\n%s", err, out)
	}
}
