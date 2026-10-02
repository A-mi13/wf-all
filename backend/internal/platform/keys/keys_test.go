package keys_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"wf/backend/internal/platform/keys"
)

func TestDecodeAcceptsAllBase64Forms(t *testing.T) {
	raw := bytes.Repeat([]byte{0xfb, 0xff, 0x01}, 11) // 33 байта: есть символы + / и - _
	for name, enc := range map[string]*base64.Encoding{
		"std": base64.StdEncoding, "raw std": base64.RawStdEncoding,
		"url": base64.URLEncoding, "raw url": base64.RawURLEncoding,
	} {
		got, err := keys.Decode(enc.EncodeToString(raw))
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("%s: %v %x", name, err, got)
		}
	}
	if _, err := keys.Decode("не base64!"); err == nil {
		t.Fatal("мусор принят")
	}
	if _, err := keys.Decode(""); err == nil {
		t.Fatal("пустой ключ принят")
	}
}

func TestIDIsStableShortAndDistinct(t *testing.T) {
	a, b := []byte("ключ-а"), []byte("ключ-б")
	// Детерминизм: ID зависит только от содержимого ключа, а не от вызова. Копия среза —
	// чтобы сравнение не свелось к одному и тому же значению и не зависело от адреса данных.
	first, second := keys.ID(a), keys.ID(append([]byte(nil), a...))
	if first != second || first == keys.ID(b) || len(first) != 11 {
		t.Fatalf("ID: %q %q %q", first, second, keys.ID(b))
	}
}

func TestGenerate(t *testing.T) {
	s, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := keys.Decode(s)
	if err != nil || len(b) != 32 {
		t.Fatalf("Generate: %v, %d байт", err, len(b))
	}
}
