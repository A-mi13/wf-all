// Package peer — кто на том конце запроса (спека бэкенда §8.4): IP клиента и метка устройства
// браузера. Заголовкам верим только от доверенных: X-Forwarded-For — от прокси хостинга
// (TrustedProxies), X-WF-Client-IP и X-WF-Device — от BFF (адрес из BFFNets и верный секрет).
// Прямой клиент подделать свой IP не может: его заголовки не читаются.
package peer

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

const (
	HeaderClientIP  = "X-WF-Client-IP"
	HeaderDevice    = "X-WF-Device"
	HeaderBFFSecret = "X-WF-BFF-Secret"
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

type Config struct {
	TrustedProxies []netip.Prefix // балансировщик хостинга: им верим X-Forwarded-For
	BFFNets        []netip.Prefix // адреса BFF (Next.js)
	BFFSecrets     []string       // общий секрет BFF ↔ API, списком для ротации
}

// Validate — до старта: слабый секрет или BFF без секрета — конфигурация ошибочна.
func (c Config) Validate() error {
	var errs []error
	if len(c.BFFNets) > 0 && len(c.BFFSecrets) == 0 {
		errs = append(errs, errors.New("peer: адреса BFF заданы, а секрета нет"))
	}
	for i, s := range c.BFFSecrets {
		if len(s) < minSecretLen {
			errs = append(errs, fmt.Errorf("peer: секрет BFF №%d короче %d символов", i+1, minSecretLen))
		}
	}
	return errors.Join(errs...)
}

type infoKey struct{}

func With(ctx context.Context, i Info) context.Context { return context.WithValue(ctx, infoKey{}, i) }

func From(ctx context.Context) Info {
	i, _ := ctx.Value(infoKey{}).(Info)
	return i
}

func Middleware(c Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info := resolve(c, r)
			// секрет дальше не нужен никому — и в логи не попадёт
			r.Header.Del(HeaderBFFSecret)
			next.ServeHTTP(w, r.WithContext(With(r.Context(), info)))
		})
	}
}

func resolve(c Config, r *http.Request) Info {
	var addr netip.Addr
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		addr = ap.Addr().Unmap()
	}
	if contains(c.TrustedProxies, addr) {
		addr = forwarded(r.Header.Values(headerForwarded), c.TrustedProxies, addr)
	}
	info := Info{IP: addr}
	if !contains(c.BFFNets, addr) || !secretOK(r.Header.Get(HeaderBFFSecret), c.BFFSecrets) {
		return info
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(HeaderClientIP)))
	if err != nil {
		return info
	}
	return Info{IP: ip.Unmap(), ViaBFF: true, Device: device(r.Header.Get(HeaderDevice))}
}

// forwarded — справа налево до первого недоверенного адреса: левее него клиент мог дописать
// что угодно. Неразбираемая запись обрывает поиск — верим последнему разобранному.
func forwarded(values []string, trusted []netip.Prefix, peer netip.Addr) netip.Addr {
	var hops []string
	for _, v := range values {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer
		}
		peer = ip.Unmap()
		if !contains(trusted, peer) {
			return peer
		}
	}
	return peer
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
