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
// default); ключи — IP из peer, пользователь из Principal, устройство (сессии или метка BFF).
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
				checks = append(checks, check{"ip:" + class + ":" + info.IP.String(), cp.IP})
			}
			device := info.Device
			if p, ok := auth.From(ctx); ok {
				checks = append(checks, check{"user:" + class + ":" + p.UserID.String(), cp.User})
				if p.DeviceID != uuid.Nil {
					device = p.DeviceID.String()
				}
			}
			if device != "" {
				checks = append(checks, check{"device:" + class + ":" + device, cp.Device})
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
