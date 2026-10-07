package keys_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"wf/backend/internal/platform/keys"
)

// seq — материал ключа 00 01 02 … (n-1) + shift: ключи теста без литералов (gitleaks).
func seq(n int, shift byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i) + shift //nolint:gosec // i < n ≤ 64: материал теста
	}
	return b
}

func enc(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func ring(t *testing.T, encoded ...string) *keys.Ring {
	t.Helper()
	r, err := keys.NewRing(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Векторы закрепляют формат: вывод ключей HKDF-SHA-256 (метки wf/keys/ring/v1/…), раскладку
// версия ‖ nonce ‖ шифротекст ‖ тег и имя ключа. Изменился формат — старые строки базы не
// расшифруются: менять только с новой версией формата и перешифровкой.
func TestRingVectors(t *testing.T) {
	r := ring(t, enc(seq(32, 0)))
	if r.CurrentID() != "Yw3NKWbEM2Y" {
		t.Fatalf("имя ключа %q", r.CurrentID())
	}
	mac, err := r.MAC(r.CurrentID(), "staff-invite", []byte("123456"))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(mac); got != "ab9486768d84ab3b90df53cde645f908a319234c1346ff6f16eaa4024935c1c7" {
		t.Fatalf("MAC %s", got)
	}
	// nonce a0…ab, aad «aad-строки»
	sealed := unhex(t, "01a0a1a2a3a4a5a6a7a8a9aaabcb70b26e63c8b5623af628ba712865d7d48808e417fa2ab97f9c25f79afba44fe8")
	pt, err := r.Open(sealed, "Yw3NKWbEM2Y", []byte("aad-строки"))
	if err != nil || string(pt) != "секрет TOTP" {
		t.Fatalf("Open вектора: %q %v", pt, err)
	}
}

func TestRingSealOpenRoundTrip(t *testing.T) {
	r := ring(t, enc(seq(32, 0)))
	for _, pt := range [][]byte{nil, {}, []byte("x"), bytes.Repeat([]byte{0xff}, 4096)} {
		ct, kid, err := r.Seal(pt, []byte("row-1"))
		if err != nil || kid != r.CurrentID() {
			t.Fatalf("Seal: %v %q", err, kid)
		}
		got, err := r.Open(ct, kid, []byte("row-1"))
		if err != nil || !bytes.Equal(got, pt) {
			t.Fatalf("Open(%d байт): %v", len(pt), err)
		}
	}
	// случайный nonce: один и тот же текст шифруется по-разному
	a, _, _ := r.Seal([]byte("same"), nil)
	b, _, _ := r.Seal([]byte("same"), nil)
	if bytes.Equal(a, b) {
		t.Fatal("одинаковый шифротекст — nonce повторяется")
	}
}

func TestRingOpenRejects(t *testing.T) {
	r := ring(t, enc(seq(32, 0)))
	ct, kid, err := r.Seal([]byte("секрет"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) []byte {
		c := bytes.Clone(ct)
		c[i] ^= 1
		return c
	}
	cases := map[string]struct {
		ct  []byte
		kid string
		aad string
		err error
	}{
		"чужой aad":           {ct, kid, "row-2", keys.ErrDecrypt},
		"без aad":             {ct, kid, "", keys.ErrDecrypt},
		"испорчена версия":    {flip(0), kid, "row-1", keys.ErrDecrypt},
		"испорчен nonce":      {flip(1), kid, "row-1", keys.ErrDecrypt},
		"испорчен шифротекст": {flip(14), kid, "row-1", keys.ErrDecrypt},
		"испорчен тег":        {flip(len(ct) - 1), kid, "row-1", keys.ErrDecrypt},
		"обрезан":             {ct[:len(ct)-1], kid, "row-1", keys.ErrDecrypt},
		"короче заголовка":    {ct[:12], kid, "row-1", keys.ErrDecrypt},
		"пусто":               {nil, kid, "row-1", keys.ErrDecrypt},
		"неизвестный ключ":    {ct, "nope", "row-1", keys.ErrUnknownKey},
	}
	for name, c := range cases {
		if _, err := r.Open(c.ct, c.kid, []byte(c.aad)); !errors.Is(err, c.err) {
			t.Errorf("%s: %v, нужно %v", name, err, c.err)
		}
	}
}

// Ротация: новый ключ первым шифрует, старый — только открывает своё; без старого ключа старые
// строки — ErrUnknownKey (сначала перешифровка, потом удаление ключа).
func TestRingRotation(t *testing.T) {
	oldKey, newKey := enc(seq(32, 0)), enc(seq(32, 100))
	before := ring(t, oldKey)
	ct, oldID, err := before.Seal([]byte("секрет"), []byte("row"))
	if err != nil {
		t.Fatal(err)
	}
	oldMAC, _ := before.MAC(oldID, "staff-recovery", []byte("abcdefghij"))

	rotated := ring(t, newKey, oldKey)
	if rotated.CurrentID() == oldID || !rotated.Has(oldID) || !rotated.Has(rotated.CurrentID()) {
		t.Fatalf("текущий %q, старый %q", rotated.CurrentID(), oldID)
	}
	if pt, err := rotated.Open(ct, oldID, []byte("row")); err != nil || string(pt) != "секрет" {
		t.Fatalf("старый ключ не открывает своё: %v", err)
	}
	if _, kid, _ := rotated.Seal([]byte("x"), nil); kid != rotated.CurrentID() {
		t.Fatalf("шифрует не текущий: %q", kid)
	}
	// MAC строки проверяется ключом строки, а не текущим
	again, err := rotated.MAC(oldID, "staff-recovery", []byte("abcdefghij"))
	if err != nil || !bytes.Equal(again, oldMAC) {
		t.Fatalf("MAC старым ключом после ротации: %v", err)
	}
	if cur, _ := rotated.MAC(rotated.CurrentID(), "staff-recovery", []byte("abcdefghij")); bytes.Equal(cur, oldMAC) {
		t.Fatal("MAC разных ключей совпал")
	}

	after := ring(t, newKey)
	if after.Has(oldID) {
		t.Fatal("удалённый ключ всё ещё в кольце")
	}
	if _, err := after.Open(ct, oldID, []byte("row")); !errors.Is(err, keys.ErrUnknownKey) {
		t.Fatalf("без старого ключа: %v", err)
	}
	if _, err := after.MAC(oldID, "staff-recovery", nil); !errors.Is(err, keys.ErrUnknownKey) {
		t.Fatalf("MAC без старого ключа: %v", err)
	}
}

// Метки независимы: MAC одного сообщения под разными метками различается, а ключ MAC — не ключ
// шифрования (вывод HKDF с разными info).
func TestRingMACLabels(t *testing.T) {
	r := ring(t, enc(seq(32, 0)))
	kid := r.CurrentID()
	a, _ := r.MAC(kid, "staff-invite", []byte("123456"))
	b, _ := r.MAC(kid, "staff-recovery", []byte("123456"))
	c, _ := r.MAC(kid, "staff-invite", []byte("123457"))
	if bytes.Equal(a, b) || bytes.Equal(a, c) || len(a) != 32 {
		t.Fatalf("метки или сообщения не различаются: %x %x %x", a, b, c)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MAC без метки не паникует")
		}
	}()
	_, _ = r.MAC(kid, "", nil)
}

func TestNewRingRejects(t *testing.T) {
	cases := map[string][]string{
		"пусто":         nil,
		"не base64":     {"не base64!"},
		"короткий":      {enc(seq(31, 0))},
		"повтор":        {enc(seq(32, 0)), enc(seq(32, 0))},
		"повтор иначе":  {enc(seq(32, 0)), base64.RawURLEncoding.EncodeToString(seq(32, 0))},
		"пустой второй": {enc(seq(32, 0)), ""},
	}
	for name, in := range cases {
		if _, err := keys.NewRing(in); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
	// длиннее 32 байт — можно (ключ выводится HKDF), формат base64 — любой из keys.Decode
	if _, err := keys.NewRing([]string{base64.RawURLEncoding.EncodeToString(seq(48, 0)), " " + enc(seq(32, 7)) + " "}); err != nil {
		t.Fatalf("годные ключи: %v", err)
	}
	// ошибка называет номер ключа, но не печатает материал
	_, err := keys.NewRing([]string{enc(seq(32, 0)), enc(seq(16, 0))})
	if err == nil || !strings.Contains(err.Error(), "ключ 2") || strings.Contains(err.Error(), enc(seq(16, 0))) {
		t.Fatalf("ошибка: %v", err)
	}
}
