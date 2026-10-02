package token_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/token"
)

func seed(t *testing.T) string {
	t.Helper()
	s, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func issuer(t *testing.T, c *clocktest.Fake, seeds ...string) *token.Issuer {
	t.Helper()
	k, err := token.ParseKeys(seeds)
	if err != nil {
		t.Fatal(err)
	}
	return token.NewIssuer(k, c)
}

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func TestAccessRoundTrip(t *testing.T) {
	c := clocktest.New(start)
	iss := issuer(t, c, seed(t))
	uid, sid := uuid.New(), uuid.New()
	raw, exp, err := iss.Access(uid, sid)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(start.Add(token.AccessTTL)) {
		t.Fatalf("срок = %v", exp)
	}
	got, err := iss.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != uid || got.SessionID != sid || !got.ExpiresAt.Equal(exp) || !got.IssuedAt.Equal(start) {
		t.Fatalf("claims: %+v", got)
	}
}

// Ротация: токен старого ключа проверяется, пока старый сид в списке; подписывает новый.
func TestRotation(t *testing.T) {
	c := clocktest.New(start)
	oldSeed, newSeed := seed(t), seed(t)
	old := issuer(t, c, oldSeed)
	raw, _, err := old.Access(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	rotated := issuer(t, c, newSeed, oldSeed)
	if _, err := rotated.Verify(raw); err != nil {
		t.Fatalf("токен старого ключа отвергнут при ротации: %v", err)
	}
	fresh, _, err := rotated.Access(uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Verify(fresh); err != nil {
		t.Fatalf("токен нового ключа отвергнут: %v", err)
	}
	if _, err := old.Verify(fresh); err == nil {
		t.Fatal("подписал не первый ключ списка")
	}
	if _, err := issuer(t, c, newSeed).Verify(raw); !errors.Is(err, token.ErrInvalid) {
		t.Fatalf("ключ выведен из ротации, а токен принят: %v", err)
	}
}

// Review Focus 4: ни одна подделка не проходит.
func TestVerifyRejects(t *testing.T) {
	c := clocktest.New(start)
	s := seed(t)
	iss := issuer(t, c, s)
	good, _, _ := iss.Access(uuid.New(), uuid.New())
	sd, _ := keys.Decode(s)
	priv := ed25519.NewKeyFromSeed(sd)
	kid := keys.ID(priv.Public().(ed25519.PublicKey))

	sign := func(method jwt.SigningMethod, key any, claims jwt.MapClaims, kidHeader string) string {
		t.Helper()
		tk := jwt.NewWithClaims(method, claims)
		if kidHeader != "" {
			tk.Header["kid"] = kidHeader
		}
		raw, err := tk.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := func() jwt.MapClaims {
		return jwt.MapClaims{"sub": uuid.NewString(), "sid": uuid.NewString(), "aud": "public",
			"iat": start.Unix(), "exp": start.Add(time.Minute).Unix()}
	}
	b64 := base64.RawURLEncoding.EncodeToString
	parts := strings.Split(good, ".")

	cases := map[string]string{
		"alg=none": b64([]byte(`{"alg":"none","kid":"`+kid+`"}`)) + "." + parts[1] + ".",
		// HS256 с открытым ключом как секретом — классическая подмена алгоритма
		"HS256 на открытом ключе": sign(jwt.SigningMethodHS256, []byte(priv.Public().(ed25519.PublicKey)), valid(), kid),
		"чужой aud": sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims {
			m := valid()
			m["aud"] = "admin"
			return m
		}(), kid),
		"неизвестный kid":     sign(jwt.SigningMethodEdDSA, priv, valid(), "unknownkid0"),
		"без kid":             sign(jwt.SigningMethodEdDSA, priv, valid(), ""),
		"без iat":             sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); delete(m, "iat"); return m }(), kid),
		"без aud":             sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); delete(m, "aud"); return m }(), kid),
		"без exp":             sign(jwt.SigningMethodEdDSA, priv, jwt.MapClaims{"sub": uuid.NewString(), "sid": uuid.NewString(), "aud": "public", "iat": start.Unix()}, kid),
		"sub не UUID":         sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); m["sub"] = "42"; return m }(), kid),
		"без sid":             sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); delete(m, "sid"); return m }(), kid),
		"iat в будущем":       sign(jwt.SigningMethodEdDSA, priv, func() jwt.MapClaims { m := valid(); m["iat"] = start.Add(time.Hour).Unix(); return m }(), kid),
		"подменённый payload": parts[0] + "." + b64([]byte(`{"sub":"`+uuid.NewString()+`","sid":"`+uuid.NewString()+`","aud":"public","exp":9999999999}`)) + "." + parts[2],
		"мусор":               "abc",
		"пусто":               "",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := iss.Verify(raw); !errors.Is(err, token.ErrInvalid) {
				t.Fatalf("принят или не ErrInvalid: %v", err)
			}
		})
	}

	t.Run("просрочен", func(t *testing.T) {
		c.Set(start.Add(token.AccessTTL + time.Minute))
		defer c.Set(start)
		if _, err := iss.Verify(good); !errors.Is(err, token.ErrInvalid) {
			t.Fatalf("просроченный принят: %v", err)
		}
	})

	// Границы leeway (5 с): расхождение часов в 3 с прощается, 6 с после срока — уже нет.
	t.Run("iat +3 с принимается", func(t *testing.T) {
		m := valid()
		m["iat"] = start.Add(3 * time.Second).Unix()
		if _, err := iss.Verify(sign(jwt.SigningMethodEdDSA, priv, m, kid)); err != nil {
			t.Fatalf("iat в пределах leeway отвергнут: %v", err)
		}
	})
	t.Run("exp +6 с отвергается", func(t *testing.T) {
		c.Set(start.Add(token.AccessTTL + 6*time.Second))
		defer c.Set(start)
		if _, err := iss.Verify(good); !errors.Is(err, token.ErrInvalid) {
			t.Fatalf("истёкший за пределами leeway принят: %v", err)
		}
	})
}

func TestParseKeys(t *testing.T) {
	if _, err := token.ParseKeys(nil); err == nil {
		t.Fatal("пустой список принят")
	}
	if _, err := token.ParseKeys([]string{base64.StdEncoding.EncodeToString([]byte("short"))}); err == nil {
		t.Fatal("сид не 32 байта принят")
	}
	s := seed(t)
	if _, err := token.ParseKeys([]string{s, s}); err == nil {
		t.Fatal("повтор ключа в списке принят")
	}
}
