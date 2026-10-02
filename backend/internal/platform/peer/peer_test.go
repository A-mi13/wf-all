package peer_test

import (
	"context"
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
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
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
		// ротация: старый секрет ещё действует
		{"BFF со старым секретом", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: strings.Repeat("o", 32), peer.HeaderClientIP: "198.51.100.7"}}, "198.51.100.7", true, ""},
		{"BFF с неверным секретом", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: strings.Repeat("x", 32), peer.HeaderClientIP: "198.51.100.7"}}, "192.0.2.10", false, ""},
		{"верный секрет не с адреса BFF", req{"203.0.113.5:4000", bff}, "203.0.113.5", false, ""},
		{"BFF с битым X-WF-Client-IP", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "nope"}}, "192.0.2.10", false, ""},
		{"IPv4 в IPv6-обёртке", req{"[::ffff:203.0.113.5]:4000", nil}, "203.0.113.5", false, ""},
		{"IPv6", req{"[2001:db8::1]:4000", nil}, "2001:db8::1", false, ""},
		{"метка устройства длиннее 128 — отброшена", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret,
			peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: strings.Repeat("d", 129)}}, "198.51.100.7", true, ""},
		{"метка устройства ровно 128 — принята", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret,
			peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: strings.Repeat("d", 128)}}, "198.51.100.7", true, strings.Repeat("d", 128)},
		{"метка устройства с пробелом — отброшена", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret,
			peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: "web dev"}}, "198.51.100.7", true, ""},
		{"метка устройства с управляющим символом — отброшена", req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret,
			peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: "web\x01dev"}}, "198.51.100.7", true, ""},
		{"метка устройства не от BFF — игнорируется", req{"203.0.113.5:4000", map[string]string{peer.HeaderDevice: "web-dev-1"}}, "203.0.113.5", false, ""},
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

// Заголовки BFF не уходят дальше по конвейеру (логи, хендлеры) ни при каком исходе:
// их значения уже в Info, а секрет не нужен никому.
func TestBFFHeadersRemoved(t *testing.T) {
	all := func(s string) map[string]string {
		return map[string]string{peer.HeaderBFFSecret: s, peer.HeaderClientIP: "198.51.100.7", peer.HeaderDevice: "web-dev-1"}
	}
	cases := []struct {
		name string
		r    req
	}{
		{"верный секрет", req{"192.0.2.10:5000", all(secret)}},
		{"неверный секрет", req{"192.0.2.10:5000", all(strings.Repeat("x", 32))}},
		{"не с адреса BFF", req{"203.0.113.5:4000", all(secret)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, h := resolve(t, cfg, c.r)
			for _, k := range []string{peer.HeaderBFFSecret, peer.HeaderClientIP, peer.HeaderDevice} {
				if h.Get(k) != "" {
					t.Fatalf("заголовок %s дошёл до хендлера", k)
				}
			}
		})
	}
}

// Без BFF в конфиге заголовки BFF не действуют вообще.
func TestNoBFFConfigured(t *testing.T) {
	got, _ := resolve(t, peer.Config{}, req{"192.0.2.10:5000", map[string]string{peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}})
	if got.ViaBFF || got.IP != netip.MustParseAddr("192.0.2.10") {
		t.Fatalf("got %+v", got)
	}
}

// Заголовок CDN (CF-Connecting-IP) от доверенного прокси: на Render перед балансировщиком стоит
// Cloudflare, и X-Forwarded-For приходит как «клиент, узел Cloudflare[, 10.x Render]».
func TestClientIPHeader(t *testing.T) {
	cdn := cfg
	cdn.ClientIPHeader = "CF-Connecting-IP"
	render := func(cf string) req {
		return req{"10.0.0.5:4000", map[string]string{"X-Forwarded-For": "203.0.113.7, 104.16.0.1", "CF-Connecting-IP": cf}}
	}
	cases := []struct {
		name string
		c    peer.Config
		r    req
		ip   string
	}{
		{"Render-цепочка: адрес из заголовка", cdn, render("203.0.113.7"), "203.0.113.7"},
		// пин старого поведения: без настройки клиентом становится узел Cloudflare — общий для всех за ним
		{"без настройки — узел CDN из X-Forwarded-For", cfg, render("203.0.113.7"), "104.16.0.1"},
		{"имя заголовка без учёта регистра", peer.Config{TrustedProxies: cfg.TrustedProxies, ClientIPHeader: "cf-connecting-ip"},
			render("203.0.113.7"), "203.0.113.7"},
		// клиент мимо прокси не выбирает себе IP
		{"подделка: пир не доверенный", cdn, req{"198.51.100.9:4000", map[string]string{"CF-Connecting-IP": "1.2.3.4"}}, "198.51.100.9"},
		{"подделка: пир не доверенный, с X-Forwarded-For", cdn,
			req{"198.51.100.9:4000", map[string]string{"CF-Connecting-IP": "1.2.3.4", "X-Forwarded-For": "1.2.3.4"}}, "198.51.100.9"},
		// не годится — запасной путь X-Forwarded-For
		{"два адреса через запятую", cdn, render("1.2.3.4, 5.6.7.8"), "104.16.0.1"},
		{"адрес с портом", cdn, render("1.2.3.4:80"), "104.16.0.1"},
		{"мусор", cdn, render("abc"), "104.16.0.1"},
		{"пустое значение", cdn, render(""), "104.16.0.1"},
		{"IPv6 с зоной", cdn, render("fe80::1%eth0"), "104.16.0.1"},
		{"заголовка нет", cdn, req{"10.0.0.5:4000", map[string]string{"X-Forwarded-For": "203.0.113.7, 104.16.0.1"}}, "104.16.0.1"},
		{"ни заголовка, ни X-Forwarded-For — пир", cdn, req{"10.0.0.5:4000", nil}, "10.0.0.5"},
		{"пробелы вокруг адреса", cdn, render("  203.0.113.7 "), "203.0.113.7"},
		{"IPv6", cdn, render("2001:db8::7"), "2001:db8::7"},
		{"IPv4 в IPv6-обёртке", cdn, render("::ffff:203.0.113.7"), "203.0.113.7"},
		// защита в глубину: CDN передаёт публичный адрес посетителя; частный, петля, нулевой — не от CDN
		{"частный IPv4", cdn, render("10.1.2.3"), "104.16.0.1"},
		{"частный IPv4 в IPv6-обёртке", cdn, render("::ffff:192.168.1.1"), "104.16.0.1"},
		{"петля IPv4", cdn, render("127.0.0.1"), "104.16.0.1"},
		{"петля IPv6", cdn, render("::1"), "104.16.0.1"},
		{"нулевой IPv4", cdn, render("0.0.0.0"), "104.16.0.1"},
		{"нулевой IPv6", cdn, render("::"), "104.16.0.1"},
		{"частный IPv6 (ULA)", cdn, render("fd00::1"), "104.16.0.1"},
		{"link-local IPv6 без зоны", cdn, render("fe80::1"), "104.16.0.1"},
		{"multicast", cdn, render("224.0.0.1"), "104.16.0.1"},
		// Cloudflare Pseudo IPv4 (режим overwrite) кладёт IPv6-клиентам адреса из 240.0.0.0/4: отказ
		// им отправил бы IPv6-клиентов в общую корзину узла CDN
		{"Pseudo IPv4 Cloudflare (240/4)", cdn, render("240.0.0.1"), "240.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := resolve(t, c.c, c.r)
			if got.IP != netip.MustParseAddr(c.ip) || got.ViaBFF {
				t.Fatalf("got %+v, want ip=%s", got, c.ip)
			}
		})
	}
}

// Две строки заголовка — не «ровно один IP»: CDN ставит одну, затирая присланное клиентом.
func TestClientIPHeaderRepeated(t *testing.T) {
	cdn := cfg
	cdn.ClientIPHeader = "CF-Connecting-IP"
	var got peer.Info
	h := peer.Middleware(cdn)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = peer.From(r.Context()) }))
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:4000"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 104.16.0.1")
	r.Header.Add("CF-Connecting-IP", "1.2.3.4")
	r.Header.Add("CF-Connecting-IP", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got.IP != netip.MustParseAddr("104.16.0.1") {
		t.Fatalf("got %+v — ждали запасной путь X-Forwarded-For", got)
	}
}

// BFF за CDN: адрес BFF берётся из заголовка CDN, дальше — прежняя проверка секрета.
func TestClientIPHeaderThenBFF(t *testing.T) {
	cdn := cfg
	cdn.ClientIPHeader = "CF-Connecting-IP"
	got, _ := resolve(t, cdn, req{"10.0.0.5:4000", map[string]string{"X-Forwarded-For": "104.16.0.1", "CF-Connecting-IP": "192.0.2.10",
		peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}})
	if got.IP != netip.MustParseAddr("198.51.100.7") || !got.ViaBFF {
		t.Fatalf("got %+v", got)
	}
}

// Подставной заголовок CDN с адресом BFF из частной сети не делает запрос «от BFF»: частный адрес
// из заголовка отвергается, адрес — из X-Forwarded-For, секрет BFF не помогает.
func TestClientIPHeaderPrivateBFFAddrRejected(t *testing.T) {
	c := peer.Config{
		TrustedProxies: cfg.TrustedProxies,
		BFFNets:        []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")},
		BFFSecrets:     cfg.BFFSecrets,
		ClientIPHeader: "CF-Connecting-IP",
	}
	got, _ := resolve(t, c, req{"10.0.0.5:4000", map[string]string{"X-Forwarded-For": "203.0.113.7, 104.16.0.1",
		"CF-Connecting-IP": "192.168.1.10", peer.HeaderBFFSecret: secret, peer.HeaderClientIP: "198.51.100.7"}})
	if got.ViaBFF || got.IP != netip.MustParseAddr("104.16.0.1") {
		t.Fatalf("got %+v — ждали 104.16.0.1 без BFF", got)
	}
}

func TestConfigValidateClientIPHeader(t *testing.T) {
	ok := peer.Config{TrustedProxies: cfg.TrustedProxies, ClientIPHeader: "CF-Connecting-IP"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]string{
		"пробел":         "CF Connecting-IP",
		"двоеточие":      "CF-Connecting-IP:",
		"скобка":         "CF(IP)",
		"не ASCII":       "Адрес",
		"перевод строки": "CF-Connecting-IP\n",
		// заголовки BFF проверяются отдельно (адрес BFF и секрет) и снимаются middleware
		"заголовок BFF с IP":          peer.HeaderClientIP,
		"он же строчными":             strings.ToLower(peer.HeaderClientIP),
		"заголовок BFF с секретом":    peer.HeaderBFFSecret,
		"заголовок BFF с устройством": peer.HeaderDevice,
		// X-Forwarded-For дописывается прокси, а не затирается: его первая строка — от клиента
		"X-Forwarded-For": "x-forwarded-for",
		// клиент присылает их сам, Cloudflare их не ставит и не затирает
		"Forwarded": "Forwarded",
		"forwarded": "forwarded",
		"X-Real-IP": "X-Real-IP",
		"x-real-ip": "x-real-ip",
	} {
		bad := ok
		bad.ClientIPHeader = h
		if bad.Validate() == nil {
			t.Errorf("%s: %q принят", name, h)
		}
	}
	// заголовку некому доверять: без прокси хостинга его прислал бы сам клиент
	if (peer.Config{ClientIPHeader: "CF-Connecting-IP"}).Validate() == nil {
		t.Error("заголовок без TrustedProxies принят")
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
	// префикс /0 сделал бы доверенным весь интернет; нулевой Prefix — ошибка разбора конфига
	for name, bad := range map[string]peer.Config{
		"/0 в TrustedProxies":      {TrustedProxies: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}},
		"IPv6 /0 в TrustedProxies": {TrustedProxies: []netip.Prefix{netip.MustParsePrefix("::/0")}},
		"невалидный в Trusted":     {TrustedProxies: []netip.Prefix{{}}},
		"/0 в BFFNets":             {BFFNets: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, BFFSecrets: cfg.BFFSecrets},
		"невалидный в BFFNets":     {BFFNets: []netip.Prefix{{}}, BFFSecrets: cfg.BFFSecrets},
	} {
		if bad.Validate() == nil {
			t.Fatalf("%s принят", name)
		}
	}
}
