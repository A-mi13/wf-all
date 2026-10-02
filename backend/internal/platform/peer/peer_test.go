package peer_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"wf/backend/internal/platform/peer"
)

var (
	secret = strings.Repeat("s", 32)
	cfg    = peer.Config{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		BFFNets:        []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
		BFFSecrets:     []string{strings.Repeat("o", 32), secret}, // ротация: старый и новый
	}
)

type req struct {
	remote  string
	headers map[string]string
}

func resolve(t *testing.T, c peer.Config, r req) (peer.Info, http.Header) {
	t.Helper()
	var got peer.Info
	var seen http.Header
	h := peer.Middleware(c)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, seen = peer.From(r.Context()), r.Header.Clone()
	}))
	hr := httptest.NewRequest(http.MethodGet, "/", nil)
	hr.RemoteAddr = r.remote
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), hr)
	return got, seen
}

func TestResolve(t *testing.T) {
	bff := map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: "web-dev-1"}
	cases := []struct {
		name   string
		r      req
		ip     string
		viaBFF bool
		device string
	}{
		{"прямой клиент", req{"203.0.113.5:4000", nil}, "203.0.113.5", false, ""},
		// Review Focus 1: подделка заголовков прямым клиентом не работает
		{"прямой клиент с X-WF-Client-IP", req{"203.0.113.5:4000", map[string]string{peer.HeaderClientIP: "1.1.1.1"}}, "203.0.113.5", false, ""},
		{"прямой клиент с X-Forwarded-For", req{"203.0.113.5:4000", map[string]string{"X-Forwarded-For": "1.1.1.1"}}, "203.0.113.5", false, ""},
		{"за прокси хостинга", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "203.0.113.9"}}, "203.0.113.9", false, ""},
		// клиент дописал левые адреса слева — берём правый недоверенный
		{"левые адреса в X-Forwarded-For", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "1.1.1.1, 203.0.113.9, 10.9.9.9"}}, "203.0.113.9", false, ""},
		{"прокси без X-Forwarded-For", req{"10.1.2.3:4000", nil}, "10.1.2.3", false, ""},
		{"мусор в X-Forwarded-For — до мусора", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "203.0.113.9, nonsense"}}, "10.1.2.3", false, ""},
		{"BFF с верным секретом", req{"192.0.2.10:5000", bff}, "198.51.100.7", true, "web-dev-1"},
		{"BFF за прокси хостинга", req{"10.1.2.3:4000", map[string]string{"X-Forwarded-For": "192.0.2.10",
			peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}}, "198.51.100.7", true, ""},
		{"BFF с неверным секретом", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: strings.Repeat("x", 32), peer.HeaderClientIP: "198.51.100.7"}}, "192.0.2.10", false, ""},
		{"верный секрет не с адреса BFF", req{"203.0.113.5:4000", bff}, "203.0.113.5", false, ""},
		{"BFF с битым X-WF-Client-IP", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "nope"}}, "192.0.2.10", false, ""},
		{"IPv4 в IPv6-обёртке", req{"[::ffff:203.0.113.5]:4000", nil}, "203.0.113.5", false, ""},
		{"IPv6", req{"[2001:db8::1]:4000", nil}, "2001:db8::1", false, ""},
		{"метка устройства длиннее 128 — отброшена", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret,
			peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: strings.Repeat("d", 129)}}, "198.51.100.7", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := resolve(t, cfg, c.r)
			if got.IP != netip.MustParseAddr(c.ip) || got.ViaBFF != c.viaBFF || got.Device != c.device {
				t.Fatalf("got %+v, want ip=%s viaBFF=%v device=%q", got, c.ip, c.viaBFF, c.device)
			}
		})
	}
}

// Секрет BFF не уходит дальше по конвейеру (логи, хендлеры) ни при каком исходе.
func TestSecretHeaderRemoved(t *testing.T) {
	_, h := resolve(t, cfg, req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}})
	if h.Get(peer.HeaderBFFSecret) != "" {
		t.Fatal("секрет BFF дошёл до хендлера")
	}
}

// Без BFF в конфиге заголовки BFF не действуют вообще.
func TestNoBFFConfigured(t *testing.T) {
	got, _ := resolve(t, peer.Config{}, req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}})
	if got.ViaBFF || got.IP != netip.MustParseAddr("192.0.2.10") {
		t.Fatalf("got %+v", got)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	short := cfg
	short.BFFSecrets = []string{"short"}
	if short.Validate() == nil {
		t.Fatal("короткий секрет принят")
	}
	noSecret := peer.Config{BFFNets: cfg.BFFNets}
	if noSecret.Validate() == nil {
		t.Fatal("адреса BFF без секрета приняты")
	}
}
