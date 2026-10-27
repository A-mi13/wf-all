package ratelimit

import (
	"log/slog"
	"math"
	"net/http"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
)

// Middleware — лимит после валидации (спека §6.1). Класс — x-rate-limit операции (без него —
// default); ключи (keys.go) — IP из peer, субъект (игрок из Principal или сотрудник из auth.Staff,
// политика User класса), устройство (сессии или метка BFF).
// Маршрут не из контракта не считается. Хранилище недоступно — запрос проходит, ошибка — в
// лог: без базы не работает и остальное, а 500 на каждый запрос ничего не защищает.
func Middleware(l Limiter, r Rules, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			rt, ok := httpx.RouteFrom(req.Context())
			if !ok {
				next.ServeHTTP(w, req)
				return
			}
			class := rt.Extension("x-rate-limit")
			if class == "" {
				class = DefaultClass
			}
			cp, ok := r[class]
			if !ok {
				// страж UnknownClasses в тестах хендлеров не даёт сюда попасть
				log.ErrorContext(req.Context(), "ratelimit: класса нет в правилах", "class", class)
				cp = r[DefaultClass]
			}
			ctx := req.Context()
			info := peer.From(ctx)
			type check struct {
				key string
				p   Policy
			}
			var checks []check
			if info.IP.IsValid() {
				checks = append(checks, check{IPKey(class, info.IP), cp.IP})
			}
			// устройство сессии (s:) и метка BFF (b:) — разные пространства ключей: метка из
			// cookie, совпавшая строкой с чужим DeviceID, не расходует его лимит
			deviceKey := ""
			if info.Device != "" {
				deviceKey = BFFDeviceKey(class, info.Device)
			}
			if s, ok := auth.SubjectFrom(ctx); ok {
				checks = append(checks, check{SubjectKey(class, s), cp.User})
				if s.DeviceID != uuid.Nil {
					deviceKey = SessionDeviceKey(class, s.DeviceID)
				}
			}
			if deviceKey != "" {
				checks = append(checks, check{deviceKey, cp.Device})
			}
			for _, c := range checks {
				res, err := l.Allow(ctx, c.key, c.p)
				if err != nil {
					log.ErrorContext(ctx, "ratelimit: хранилище недоступно, запрос пропущен", "err", err)
					break
				}
				if !res.Allowed {
					httpx.WriteProblemValue(w, req, httpx.Problem{
						Status:     http.StatusTooManyRequests,
						Code:       httpx.CodeRateLimited,
						RetryAfter: int(math.Ceil(res.RetryAfter.Seconds())),
					})
					return
				}
			}
			next.ServeHTTP(w, req)
		})
	}
}
