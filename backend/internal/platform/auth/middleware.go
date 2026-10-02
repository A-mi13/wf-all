package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/token"
)

// Verifier — проверка access-токена; реализует *token.Issuer.
type Verifier interface {
	Verify(raw string) (token.Claims, error)
}

// Middleware — необязательная аутентификация (спека §6.1): нет Authorization — запрос идёт
// дальше без Principal (нужен ли вход, решает security контракта в валидаторе). Заголовок
// есть, но токен битый, просрочен или сессия недействительна — 401 сразу. Сбой загрузки
// сессии — 500: недоступная база — не повод выкидывать пользователя из приложения.
func Middleware(v Verifier, l SessionLoader, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if h == "" {
				next.ServeHTTP(w, r)
				return
			}
			scheme, raw, ok := strings.Cut(h, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(raw) == "" {
				unauthorized(w, r)
				return
			}
			claims, err := v.Verify(strings.TrimSpace(raw))
			if err != nil {
				unauthorized(w, r)
				return
			}
			p, err := l.LoadSession(r.Context(), claims.SessionID)
			switch {
			case errors.Is(err, ErrSessionInvalid):
				unauthorized(w, r)
				return
			case err != nil:
				httpx.WriteError(log, w, r, err)
				return
			case p == nil: // загрузчик нарушил договор: ни Principal, ни ошибки
				httpx.WriteError(log, w, r, errNilPrincipal)
				return
			case p.UserID != claims.UserID || p.SessionID != claims.SessionID:
				unauthorized(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(With(r.Context(), p)))
		})
	}
}

var errNilPrincipal = errors.New("auth: загрузчик сессии вернул nil без ошибки")

func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthenticated, "")
}
