package blob_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/testkit/clocktest"
)

var (
	signKey = []byte(strings.Repeat("k", 32))
	t0      = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
)

func newSigner(t *testing.T, clk *clocktest.Fake) *blob.DevSigner {
	t.Helper()
	s, err := blob.NewDevSigner(signKey, "http://127.0.0.1:8080/", clk)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDevSignerKeyLength(t *testing.T) {
	clk := clocktest.New(t0)
	if _, err := blob.NewDevSigner(signKey[:31], "http://127.0.0.1:8080", clk); err == nil {
		t.Fatal("ключ 31 байт принят")
	}
	if _, err := blob.NewDevSigner(signKey, "http://127.0.0.1:8080", clk); err != nil {
		t.Fatalf("ключ 32 байта: %v", err)
	}
	for _, base := range []string{"", "127.0.0.1:8080", "/relative", "ftp://h"} {
		if _, err := blob.NewDevSigner(signKey, base, clk); err == nil {
			t.Errorf("baseURL %q принят", base)
		}
	}
}

func TestDevSignerUpload(t *testing.T) {
	ctx := context.Background()
	clk := clocktest.New(t0)
	s := newSigner(t, clk)
	spec := blob.UploadSpec{Key: "0193a1b2-0000-7000-8000-000000000001", ContentType: "image/jpeg", Bytes: 1000, TTL: 15 * time.Minute}
	form, err := s.PresignUpload(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if form.URL != "http://127.0.0.1:8080/_blob/in" || form.MaxBytes != 1000 || !form.ExpiresAt.Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("форма: %+v", form)
	}
	if form.Fields["key"] != spec.Key || form.Fields["Content-Type"] != spec.ContentType ||
		form.Fields["policy"] == "" || form.Fields["x-wf-signature"] == "" {
		t.Fatalf("поля: %v", form.Fields)
	}
	other, err := s.PresignUpload(ctx, blob.UploadSpec{Key: "other", ContentType: "image/jpeg", Bytes: 1000, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	with := func(k, v string) map[string]string {
		f := maps.Clone(form.Fields)
		if v == "" {
			delete(f, k)
		} else {
			f[k] = v
		}
		return f
	}
	cases := []struct {
		name   string
		fields map[string]string
		size   int64
		ct     string
		at     time.Time
		want   error
	}{
		{"ok", form.Fields, 1000, "image/jpeg", t0, nil},
		{"before_expiry", form.Fields, 1000, "image/jpeg", form.ExpiresAt.Add(-time.Second), nil},
		{"size_plus_one", form.Fields, 1001, "image/jpeg", t0, blob.ErrSignature},
		{"size_minus_one", form.Fields, 999, "image/jpeg", t0, blob.ErrSignature},
		{"other_type", form.Fields, 1000, "image/png", t0, blob.ErrSignature},
		{"other_type_field", with("Content-Type", "image/png"), 1000, "image/jpeg", t0, blob.ErrSignature},
		{"no_type_field", with("Content-Type", ""), 1000, "image/jpeg", t0, blob.ErrSignature},
		{"other_key_field", with("key", "other"), 1000, "image/jpeg", t0, blob.ErrSignature},
		{"no_key_field", with("key", ""), 1000, "image/jpeg", t0, blob.ErrSignature},
		// файл — ровно под подменённую политику: отказ обязан дать подпись, а не сверка размера или срока
		{"tampered_policy", with("policy", tamper(t, form.Fields["policy"], "bytes", 1001)), 1001, "image/jpeg", t0, blob.ErrSignature},
		{"extended_exp", with("policy", tamper(t, form.Fields["policy"], "exp", form.ExpiresAt.Add(time.Hour).Unix())), 1000, "image/jpeg", form.ExpiresAt.Add(time.Second), blob.ErrSignature},
		{"foreign_policy", with("policy", other.Fields["policy"]), 1000, "image/jpeg", t0, blob.ErrSignature},
		{"no_signature", with("x-wf-signature", ""), 1000, "image/jpeg", t0, blob.ErrSignature},
		{"bad_hex", with("x-wf-signature", "zz"+form.Fields["x-wf-signature"][2:]), 1000, "image/jpeg", t0, blob.ErrSignature},
		{"extra_field", with("acl", "public-read"), 1000, "image/jpeg", t0, blob.ErrSignature},
		{"at_expiry", form.Fields, 1000, "image/jpeg", form.ExpiresAt, blob.ErrExpired},
		{"after_expiry", form.Fields, 1000, "image/jpeg", form.ExpiresAt.Add(time.Hour), blob.ErrExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk.Set(c.at)
			key, err := s.VerifyUpload(c.fields, c.size, c.ct)
			if !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
				t.Fatalf("ошибка %v, ждали %v", err, c.want)
			}
			if c.want == nil && key != spec.Key {
				t.Fatalf("ключ %q", key)
			}
		})
	}

	clk.Set(t0)
	for _, bad := range []blob.UploadSpec{
		{Key: "../x", ContentType: "image/jpeg", Bytes: 1, TTL: time.Minute},
		{Key: "x", ContentType: "", Bytes: 1, TTL: time.Minute},
		{Key: "x", ContentType: "image/jpeg", Bytes: 0, TTL: time.Minute},
		{Key: "x", ContentType: "image/jpeg", Bytes: 1, TTL: 0},
	} {
		if _, err := s.PresignUpload(ctx, bad); err == nil {
			t.Errorf("PresignUpload(%+v) принят", bad)
		}
	}
}

// tamper — политика с подменённым полем (name = value); подпись остаётся старой.
func tamper(t *testing.T, policy, name string, value int64) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(policy)
	if err != nil {
		t.Fatalf("политика не base64: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("политика не JSON: %v", err)
	}
	if _, ok := m[name]; !ok {
		t.Fatalf("в политике нет поля %q: %s", name, raw)
	}
	m[name] = value
	changed, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(changed)
}

func TestDevSignerGet(t *testing.T) {
	ctx := context.Background()
	clk := clocktest.New(t0)
	s := newSigner(t, clk)
	key := "0193a1b2-0000-7000-8000-000000000001/full.webp"
	raw, exp, err := s.PresignGet(ctx, key, 10*time.Minute, "private, max-age=600")
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(t0.Add(10 * time.Minute)) {
		t.Fatalf("срок %v", exp)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "http://127.0.0.1:8080/_media/priv/"+key {
		t.Fatalf("адрес %q", raw)
	}
	q := u.Query()
	e, sig := q.Get("exp"), q.Get("sig")
	if e != strconv.FormatInt(exp.Unix(), 10) {
		t.Fatalf("exp %q", e)
	}

	upload, err := s.PresignUpload(ctx, blob.UploadSpec{Key: "in-key", ContentType: "image/jpeg", Bytes: 5, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, key, exp, sig string
		at                  time.Time
		want                error
	}{
		{"ok", key, e, sig, t0, nil},
		{"before_expiry", key, e, sig, exp.Add(-time.Second), nil},
		{"other_key", "0193a1b2-0000-7000-8000-000000000002/full.webp", e, sig, t0, blob.ErrSignature},
		{"changed_exp", key, strconv.FormatInt(exp.Unix()+3600, 10), sig, t0, blob.ErrSignature},
		{"bad_hex", key, e, "zz" + sig[2:], t0, blob.ErrSignature},
		{"empty_sig", key, e, "", t0, blob.ErrSignature},
		{"upload_signature_reused", "in-key", e, upload.Fields["x-wf-signature"], t0, blob.ErrSignature},
		{"at_expiry", key, e, sig, exp, blob.ErrExpired},
		{"after_expiry", key, e, sig, exp.Add(time.Hour), blob.ErrExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk.Set(c.at)
			err := s.VerifyGet(c.key, c.exp, c.sig)
			if !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
				t.Fatalf("ошибка %v, ждали %v", err, c.want)
			}
		})
	}
}
