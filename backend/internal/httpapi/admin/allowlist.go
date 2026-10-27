package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
)

// allowlist — IP-аллоулист админки (ADMIN_IP_ALLOWLIST, спека сотрудников §5.1): пустой —
// выключен; адрес клиента (peer) вне всех сетей — 403 auth.ip_not_allowed. Стоит сразу за peer —
// на всё: API, /docs и статику.
func allowlist(nets []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(nets) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peer.From(r.Context()).IP.Unmap()
			for _, p := range nets {
				if p.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeIPNotAllowed, "")
		})
	}
}

// ValidateAllowlist — сети ADMIN_IP_ALLOWLIST годны: каждая — валидный префикс длиной больше 0.
// 0.0.0.0/0 и ::/0 пускают всех — это не аллоулист, а выключенная защита под видом включённой;
// выключают её пустым списком.
func ValidateAllowlist(nets []netip.Prefix) error {
	var errs []error
	for _, p := range nets {
		switch {
		case !p.IsValid():
			errs = append(errs, errors.New("пустая или неверная сеть"))
		case p.Bits() == 0:
			errs = append(errs, fmt.Errorf("%s пускает всех — выключают пустым списком", p))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("admin: ADMIN_IP_ALLOWLIST: %w", errors.Join(errs...))
	}
	return nil
}
