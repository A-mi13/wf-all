package admin

import (
	"crypto/sha256"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
)

// Имя cookie сессии сотрудника. __Host- браузер принимает только с Secure, Path=/ и без Domain —
// cookie не подменить с поддомена. Без Secure — dev-имя (R6: проверка на движках Playwright
// 13.10.2026 — Chromium и Firefox принимают __Host- с Secure на http://127.0.0.1, WebKit — нет).
const (
	cookieSecure = "__Host-wf_admin_session"
	cookieDev    = "wf_admin_session"
)

// CookieName — имя cookie сессии сотрудника; единственный источник имени (конвейер читает,
// хендлеры входа ставят и стирают): secure — ADMIN_COOKIE_SECURE.
func CookieName(secure bool) string {
	if secure {
		return cookieSecure
	}
	return cookieDev
}

// classStaffAuth — класс лимита входа в админку: в его IP-лимите считаются и отказы cookie (R9).
const classStaffAuth = "staff_auth"

// headerBackground — фоновый запрос (опрос счётчиков): не продлевает бездействие сессии (D10).
const headerBackground = "X-WF-Background"

// Негативный кэш отказов сессии (R9): срок и предел записей.
const (
	negCacheTTL = 5 * time.Second
	negCacheMax = 10_000
)

var errNilStaff = errors.New("admin: загрузчик сессии вернул nil без ошибки")

// sessionLayer — сессия сотрудника по cookie (спека сотрудников §5.1, P7, R9). Только на маршрутах
// контракта. Нет cookie — запрос идёт дальше анонимно (вход нужен — решает валидатор по security).
// Cookie есть:
//  1. IP-лимит staff_auth исчерпан — 429 ratelimit.exceeded, cookie не проверяется вовсе;
//  2. токен невалиден (ErrSessionInvalid или недавний отказ из негативного кэша) — отказ
//     считается в том же IP-лимите (Limiter.Add), ответ стирает cookie (Max-Age=0), запрос идёт
//     дальше анонимно;
//  3. сбой загрузчика — 500: недоступная база — не повод выкидывать сотрудника;
//  4. сотрудник — в ctx (auth.WithStaff) до лимита и идемпотентности.
//
// Положительного кэша нет: сессия сверяется с базой на каждый запрос.
type sessionLayer struct {
	sessions auth.StaffSessions
	cookie   string // CookieName(secure)
	secure   bool
	limiter  ratelimit.Limiter
	policy   ratelimit.Policy // IP-политика класса staff_auth
	neg      *negCache
	log      *slog.Logger
}

func (s *sessionLayer) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := httpx.RouteFrom(r.Context()); !ok {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(s.cookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		ip := peer.From(ctx).IP
		key := ratelimit.IPKey(classStaffAuth, ip)
		if ip.IsValid() {
			// хранилище лимитов недоступно — проверка пропускается, как в ratelimit.Middleware
			over, err := s.limiter.Over(ctx, key, s.policy)
			if err != nil {
				s.log.ErrorContext(ctx, "admin: лимит отказов сессии недоступен", "err", err)
			} else if over {
				httpx.WriteProblemValue(w, r, httpx.Problem{
					Status: http.StatusTooManyRequests, Code: httpx.CodeRateLimited, RetryAfter: retryAfter(s.policy),
				})
				return
			}
		}
		hash := sha256.Sum256([]byte(c.Value))
		invalid := s.neg.has(hash)
		var st *auth.Staff
		if !invalid {
			st, err = s.sessions.LoadStaff(ctx, c.Value, ip, r.Header.Get(headerBackground) == "1")
			switch {
			case errors.Is(err, auth.ErrSessionInvalid):
				invalid = true
				s.neg.put(hash)
			case err != nil:
				httpx.WriteError(s.log, w, r, err)
				return
			case st == nil: // загрузчик нарушил договор: ни сотрудника, ни ошибки
				httpx.WriteError(s.log, w, r, errNilStaff)
				return
			}
		}
		if invalid {
			if ip.IsValid() {
				if err := s.limiter.Add(ctx, key, s.policy); err != nil {
					s.log.ErrorContext(ctx, "admin: отказ сессии не учтён в лимите", "err", err)
				}
			}
			// Secure — из ADMIN_COOKIE_SECURE: false только в dev по http (R6), стирание должно
			// совпасть по атрибутам с установкой
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure по конфигу, см. выше
				Name: s.cookie, Path: "/", MaxAge: -1, // Max-Age=0: браузер удаляет сразу
				HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
			})
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithStaff(ctx, st)))
	})
}

// retryAfter — через сколько секунд IP-лимит отказов пропустит запрос: Over не говорит точного
// срока, а следующий запрос проходит не раньше одного интервала политики (период / лимит).
func retryAfter(p ratelimit.Policy) int {
	if p.Limit <= 0 {
		return 1
	}
	return max(1, int(math.Ceil((p.Period / time.Duration(p.Limit)).Seconds())))
}

// negCache — недавние отказы сессии по SHA-256 токена (сам токен не хранится): повтор того же
// токена не ходит в базу ttl. Все записи живут одинаково, поэтому порядок вставки — порядок
// истечения: очередь order вытесняет самые старые, когда записей больше max.
type negCache struct {
	clock clock.Clock
	ttl   time.Duration
	max   int

	mu    sync.Mutex
	until map[[32]byte]time.Time
	order []negEntry
}

type negEntry struct {
	key   [32]byte
	until time.Time
}

func newNegCache(c clock.Clock, ttl time.Duration, maxEntries int) *negCache {
	return &negCache{clock: c, ttl: ttl, max: maxEntries, until: make(map[[32]byte]time.Time)}
}

func (c *negCache) has(key [32]byte) bool {
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.until[key]
	if ok && !now.Before(t) {
		delete(c.until, key) // запись в очереди уйдёт при вытеснении
		return false
	}
	return ok
}

func (c *negCache) put(key [32]byte) {
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.order) > 0 && (len(c.until) >= c.max || !now.Before(c.order[0].until)) {
		e := c.order[0]
		c.order = c.order[1:]
		// в карте может быть уже другая запись того же ключа (повторная вставка) — её не трогать
		if t, ok := c.until[e.key]; ok && t.Equal(e.until) {
			delete(c.until, e.key)
		}
	}
	until := now.Add(c.ttl)
	c.until[key] = until
	c.order = append(c.order, negEntry{key, until})
}

// size — записей в кэше (тесты).
func (c *negCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.until)
}
