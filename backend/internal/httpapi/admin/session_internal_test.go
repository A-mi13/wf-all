package admin

import (
	"crypto/sha256"
	"strconv"
	"testing"
	"time"

	"wf/backend/internal/platform/testkit/clocktest"
)

func tokenHash(i int) [32]byte { return sha256.Sum256([]byte("tok-" + strconv.Itoa(i))) }

// Негативный кэш (R9): запись живёт ttl; больше max записей не бывает — вытесняются самые старые.
func TestNegCache(t *testing.T) {
	clk := clocktest.New(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	c := newNegCache(clk, 5*time.Second, 3)
	c.put(tokenHash(1))
	if !c.has(tokenHash(1)) || c.has(tokenHash(2)) {
		t.Fatal("has после put")
	}
	clk.Advance(4999 * time.Millisecond)
	if !c.has(tokenHash(1)) {
		t.Fatal("истекла раньше ttl")
	}
	clk.Advance(time.Millisecond)
	if c.has(tokenHash(1)) {
		t.Fatal("жива после ttl")
	}

	// предел: четвёртая запись вытесняет первую
	for i := range 4 {
		c.put(tokenHash(10 + i))
		clk.Advance(time.Millisecond)
	}
	if n := c.size(); n > 3 {
		t.Fatalf("записей %d при пределе 3", n)
	}
	if c.has(tokenHash(10)) {
		t.Fatal("старая запись не вытеснена")
	}
	for i := 11; i <= 13; i++ {
		if !c.has(tokenHash(i)) {
			t.Fatalf("запись %d потеряна", i)
		}
	}

	// много записей подряд — предел держится; истёкшие уходят при следующей вставке
	for i := range 1000 {
		c.put(tokenHash(100 + i))
	}
	if n := c.size(); n > 3 {
		t.Fatalf("записей %d при пределе 3", n)
	}
	clk.Advance(5 * time.Second)
	c.put(tokenHash(5000))
	if n := c.size(); n != 1 {
		t.Fatalf("после истечения записей %d, ждали 1", n)
	}
}

// Предел и срок по умолчанию — из R9: 5 с и 10 000 записей.
func TestNegCacheDefaults(t *testing.T) {
	if negCacheTTL != 5*time.Second || negCacheMax != 10_000 {
		t.Fatalf("ttl %v, max %d", negCacheTTL, negCacheMax)
	}
}
