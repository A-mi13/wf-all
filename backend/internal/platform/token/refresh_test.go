package token_test

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"wf/backend/internal/platform/token"
)

func TestNewRefresh(t *testing.T) {
	raw, hash, err := token.NewRefresh()
	if err != nil {
		t.Fatal(err)
	}
	// 256 бит в base64url без паддинга — 43 символа
	if len(raw) != 43 {
		t.Fatalf("длина = %d", len(raw))
	}
	sum := sha256.Sum256([]byte(raw))
	if !bytes.Equal(hash, sum[:]) || !bytes.Equal(token.HashRefresh(raw), hash) {
		t.Fatal("хэш не SHA-256 токена")
	}
	other, _, _ := token.NewRefresh()
	if other == raw {
		t.Fatal("два одинаковых токена")
	}
}

// Окно гонки (§6.2): преемника открывает только предъявивший тот же токен.
func TestSealSuccessor(t *testing.T) {
	presented, _, _ := token.NewRefresh()
	successor, _, _ := token.NewRefresh()
	sealed, err := token.SealSuccessor(presented, successor)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(successor)) {
		t.Fatal("преемник лежит открытым")
	}
	got, err := token.OpenSuccessor(presented, sealed)
	if err != nil || got != successor {
		t.Fatalf("открыт %q, %v", got, err)
	}
	stranger, _, _ := token.NewRefresh()
	if _, err := token.OpenSuccessor(stranger, sealed); err == nil {
		t.Fatal("чужой токен открыл преемника")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := token.OpenSuccessor(presented, sealed); err == nil {
		t.Fatal("испорченный шифротекст открылся")
	}
	if _, err := token.OpenSuccessor(presented, []byte{1, 2}); err == nil {
		t.Fatal("обрезок открылся")
	}
}
