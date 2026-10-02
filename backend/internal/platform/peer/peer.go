// Package peer — кто на том конце запроса (спека бэкенда §8.4): IP клиента и метка устройства
// браузера. Заголовкам верим только от доверенных: заголовок CDN (ClientIPHeader) и
// X-Forwarded-For — от прокси хостинга (TrustedProxies), X-WF-Client-IP и X-WF-Device — от BFF
// (адрес из BFFNets и верный секрет).
// Прямой клиент подделать свой IP не может: его заголовки не читаются.
package peer

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/textproto"
	"strings"
)

const (
	HeaderClientIP  = "X-WF-Client-IP"
	HeaderDevice    = "X-WF-Device"
	HeaderBFFSecret = "X-WF-BFF-Secret" //nolint:gosec // G101: имя HTTP-заголовка, а не значение секрета
	headerForwarded = "X-Forwarded-For"

	minSecretLen = 32
	maxDeviceLen = 128
)

// Info — собеседник запроса.
type Info struct {
	IP     netip.Addr // IP клиента; нулевой — адрес не разобран
	ViaBFF bool       // запрос пришёл от BFF с верным секретом
	Device string     // метка устройства браузера из cookie BFF; только от BFF
}

// Config — кому и в чём доверяем; пустой Config не доверяет никому.
type Config struct {
	TrustedProxies []netip.Prefix // балансировщик хостинга: им верим X-Forwarded-For
	BFFNets        []netip.Prefix // адреса BFF (Next.js)
	BFFSecrets     []string       // общий секрет BFF ↔ API, списком для ротации
	// ClientIPHeader — заголовок с адресом посетителя, который ставит CDN перед прокси хостинга
	// (CF-Connecting-IP у Cloudflare); читается только от TrustedProxies. Пусто — не читается.
	ClientIPHeader string
}

// Validate — до старта: слабый секрет, BFF без секрета, невалидный префикс или префикс /0
// (доверие всему интернету), ClientIPHeader — не имя HTTP-заголовка, заголовок BFF,
// X-Forwarded-For, Forwarded, X-Real-IP или задан без TrustedProxies — конфигурация ошибочна.
func (c Config) Validate() error {
	var errs []error
	for _, l := range []struct {
		name string
		nets []netip.Prefix
	}{{"TrustedProxies", c.TrustedProxies}, {"BFFNets", c.BFFNets}} {
		for i, n := range l.nets {
			if !n.IsValid() || n.Bits() == 0 {
				errs = append(errs, fmt.Errorf("peer: %s №%d — невалидная сеть или /0", l.name, i+1))
			}
		}
	}
	if len(c.BFFNets) > 0 && len(c.BFFSecrets) == 0 {
		errs = append(errs, errors.New("peer: адреса BFF заданы, а секрета нет"))
	}
	for i, s := range c.BFFSecrets {
		if len(s) < minSecretLen {
			errs = append(errs, fmt.Errorf("peer: секрет BFF №%d короче %d символов", i+1, minSecretLen))
		}
	}
	if c.ClientIPHeader != "" {
		errs = append(errs, c.validateClientIPHeader()...)
	}
	return errors.Join(errs...)
}

// validateClientIPHeader — имя заголовка CDN: HTTP token (RFC 9110 §5.6.2), не заголовок BFF, не
// X-Forwarded-For, Forwarded и X-Real-IP, и есть прокси хостинга, от которого его читать.
func (c Config) validateClientIPHeader() []error {
	var errs []error
	h := c.ClientIPHeader
	if !isToken(h) {
		errs = append(errs, fmt.Errorf("peer: ClientIPHeader %q — не имя HTTP-заголовка", h))
	}
	// каноническая форма с обеих сторон: X-WF-Client-IP канонически — X-Wf-Client-Ip
	switch canon := textproto.CanonicalMIMEHeaderKey; canon(h) {
	case canon(HeaderClientIP), canon(HeaderBFFSecret), canon(HeaderDevice):
		// заголовки BFF проверяются своим путём (адрес BFF и секрет) и снимаются middleware
		errs = append(errs, fmt.Errorf("peer: ClientIPHeader %q — заголовок BFF", h))
	case canon(headerForwarded):
		// прокси дописывает X-Forwarded-For, а не затирает: первая строка может быть от клиента
		errs = append(errs, fmt.Errorf("peer: ClientIPHeader %q — X-Forwarded-For уже разбирается справа налево", h))
	case "Forwarded", "X-Real-Ip":
		// клиент присылает их сам, Cloudflare их не ставит и не затирает
		errs = append(errs, fmt.Errorf("peer: ClientIPHeader %q — заголовок может прислать клиент", h))
	}
	if len(c.TrustedProxies) == 0 {
		// без прокси хостинга заголовок пришёл бы прямо от клиента
		errs = append(errs, fmt.Errorf("peer: ClientIPHeader %q задан без TrustedProxies — доверять некому", h))
	}
	return errs
}

// isToken — RFC 9110 §5.6.2: token = 1*tchar.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		b := s[i]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0:
		default:
			return false
		}
	}
	return true
}

type infoKey struct{}

// With кладёт Info в контекст — для тестов других пакетов.
func With(ctx context.Context, i Info) context.Context { return context.WithValue(ctx, infoKey{}, i) }

// From достаёт Info из контекста; если Middleware не проходил — нулевой Info.
func From(ctx context.Context) Info {
	i, _ := ctx.Value(infoKey{}).(Info)
	return i
}

// source — каким путём получен адрес клиента; значения пишутся в диагностический лог.
type source string

const (
	sourcePeer      source = "peer"      // адрес TCP-соединения (RemoteAddr)
	sourceCDN       source = "cdn"       // заголовок CDN (ClientIPHeader)
	sourceForwarded source = "forwarded" // X-Forwarded-For справа налево
	sourceBFF       source = "bff"       // X-WF-Client-IP от BFF
)

// Middleware разбирает собеседника запроса и кладёт Info в контекст. Заголовки BFF снимаются
// всегда: значения уже в Info, а дальше по конвейеру (хендлеры, логи) они только соблазн
// прочитать непроверенное.
func Middleware(c Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, src := resolve(c, r)
			// до снятия заголовков BFF: лог видит запрос таким, каким он пришёл (значения BFF не читает);
			// проверка уровня — чтобы на Info не склеивать заголовки и не собирать атрибуты на горячем пути
			if slog.Default().Enabled(r.Context(), slog.LevelDebug) {
				logPeer(r, c, info, src)
			}
			r.Header.Del(HeaderBFFSecret)
			r.Header.Del(HeaderClientIP)
			r.Header.Del(HeaderDevice)
			next.ServeHTTP(w, r.WithContext(With(r.Context(), info)))
		})
	}
}

// logPeer — диагностика источника IP за прокси и CDN: что пришло в процесс (адрес соединения,
// X-Forwarded-For, заголовок CDN) и откуда взят итоговый адрес. Включается временно —
// API_LOG_LEVEL=debug, при Info запись не создаётся и заголовки не склеиваются. IP клиента —
// персональные данные, поэтому только Debug и только на время диагностики. Секрет BFF и X-WF-Device
// не пишутся никогда. Вызывается только при включённом Debug.
func logPeer(r *http.Request, c Config, info Info, src source) {
	remote := peerAddr(r)
	attrs := []any{
		"remote_addr", r.RemoteAddr,
		"remote_trusted", contains(c.TrustedProxies, remote),
		"forwarded", strings.Join(r.Header.Values(headerForwarded), ", "),
		"cdn_header", c.ClientIPHeader,
	}
	if c.ClientIPHeader != "" {
		attrs = append(attrs, "cdn_value", strings.Join(r.Header.Values(c.ClientIPHeader), ", "))
	}
	attrs = append(attrs, "client_ip", info.IP.String(), "source", string(src))
	slog.DebugContext(r.Context(), "peer", attrs...)
}

// peerAddr — адрес TCP-соединения без IPv4-обёртки; нулевой, если RemoteAddr не разобрать.
func peerAddr(r *http.Request) netip.Addr {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().Unmap()
	}
	return netip.Addr{}
}

// resolve возвращает собеседника и путь, которым получен его адрес.
func resolve(c Config, r *http.Request) (Info, source) {
	addr, src := peerAddr(r), sourcePeer
	if contains(c.TrustedProxies, addr) {
		if ip, ok := fromCDN(r.Header, c.ClientIPHeader); ok {
			addr, src = ip, sourceCDN
		} else if vals := r.Header.Values(headerForwarded); len(vals) > 0 {
			addr, src = forwarded(vals, c.TrustedProxies, addr), sourceForwarded
		}
	}
	info := Info{IP: addr}
	if !contains(c.BFFNets, addr) || !secretOK(r.Header.Get(HeaderBFFSecret), c.BFFSecrets) {
		return info, src
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(HeaderClientIP)))
	if err != nil {
		return info, src
	}
	return Info{IP: ip.Unmap(), ViaBFF: true, Device: device(r.Header.Get(HeaderDevice))}, sourceBFF
}

// fromCDN — адрес посетителя из заголовка CDN; вызывается только для пира из TrustedProxies.
// Доверие ровно такое: верим любому пиру из TrustedProxies, был ли перед ним CDN — не проверяем.
// Расчёт на то, что CDN (Cloudflare с CF-Connecting-IP) ставит заголовок сам и затирает присланный
// клиентом, а к прокси хостинга запросы приходят только через CDN; второе — допущение (спека §8.4).
// X-Forwarded-For так не годится: прокси хостинга дописывает его, и справа от клиента стоит
// публичный узел CDN — общий для всех клиентов за ним. IP отсюда — сигнал для лимитов и риска,
// не фактор входа и прав.
// Годится ровно одна строка с голым IP без зоны — не частный, не петля, не нулевой, не
// multicast/link-local; иначе (запятые, порт, мусор, пусто, такие адреса) — false, и адрес берётся
// прежним путём из X-Forwarded-For.
func fromCDN(h http.Header, name string) (netip.Addr, bool) {
	if name == "" {
		return netip.Addr{}, false
	}
	v := h.Values(name)
	if len(v) != 1 {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(v[0]))
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, false
	}
	ip = ip.Unmap()
	// защита в глубину: частный адрес в заголовке — не от CDN (и мог бы совпасть с сетью BFF или
	// прокси); петлю, нулевой, multicast и link-local отсекает IsGlobalUnicast
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return netip.Addr{}, false
	}
	return ip, true
}

// forwarded — справа налево до первого недоверенного адреса: левее него клиент мог дописать
// что угодно. Неразбираемая запись обрывает поиск — верим последнему разобранному.
func forwarded(values []string, trusted []netip.Prefix, last netip.Addr) netip.Addr {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return last
		}
		last = ip.Unmap()
		if !contains(trusted, last) {
			return last
		}
	}
	return last
}

func contains(nets []netip.Prefix, a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	for _, n := range nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

func secretOK(got string, secrets []string) bool {
	if got == "" {
		return false
	}
	ok := 0
	for _, s := range secrets {
		ok |= subtle.ConstantTimeCompare([]byte(got), []byte(s))
	}
	return ok == 1
}

// device — метка устройства: печатный ASCII до 128 символов, иначе пусто.
func device(s string) string {
	if len(s) > maxDeviceLen {
		return ""
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return ""
		}
	}
	return s
}
