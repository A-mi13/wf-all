// Package admin — операции модуля staff в админке (тег staff контракта admin): вход, второй фактор,
// сессии, «Мой доступ», приглашения, роли и доступ сотрудников (спека сотрудников §4). Тонкий
// HTTP-слой: запрос → сценарий internal/app → ответ контракта; ошибки переводит Problem в одном
// месте (Middleware), cookie сессии ставят ответы операций входа (P8).
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

// Cookie — имя и Secure cookie сессии сотрудника. Их знает только сборка (httpapi/admin.CookieName по
// ADMIN_COOKIE_SECURE): staff/admin не импортирует httpapi/admin (archtest).
type Cookie struct {
	Name   string
	Secure bool
}

// Handler — операции тега staff; встраивается в internal/httpapi/admin.Server указателем. Собирается
// New; нулевой Handler (cmd/admin-api до сборки Task 10c) на каждую операцию тега отвечает 501
// http.not_implemented — проверка одна, в Middleware.
type Handler struct {
	app    *app.App
	cookie Cookie
}

// New — хендлер на сценариях staff (зависимости admin-api) и cookie сессии сборки.
func New(d staff.Deps, c Cookie) *Handler {
	return &Handler{app: app.New(d), cookie: c}
}

// Sessions — загрузчик сессии сотрудника по токену cookie для конвейера admin-api (Options.Sessions).
// Нулевой Handler — auth.NoStaffSessions: любой токен недействителен.
func (h *Handler) Sessions() auth.StaffSessions {
	if h.app == nil {
		return auth.NoStaffSessions
	}
	return h.app
}

// CookieConfig — cookie сессии, на которую собран хендлер; ok=false — нулевой Handler (не собран, cookie
// не ставит). Сборка сверяет её с cookie конвейера (httpapi/admin.NewHandler): рассогласование — отказ
// старта.
func (h *Handler) CookieConfig() (c Cookie, ok bool) {
	return h.cookie, h.app != nil
}

var errNotImplemented = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

// operations — имена операций тега staff: методы *Handler вида (ctx, запрос) (ответ, error). Имя
// метода strict-интерфейса совпадает с operationID, который strict-сервер передаёт middleware.
var operations = sync.OnceValue(func() map[string]bool {
	ctxType, errType := reflect.TypeFor[context.Context](), reflect.TypeFor[error]()
	t := reflect.TypeFor[*Handler]()
	ops := make(map[string]bool, t.NumMethod())
	for i := range t.NumMethod() {
		ft := t.Method(i).Type // с получателем: (h, ctx, запрос)
		if ft.NumIn() == 3 && ft.In(1) == ctxType && ft.NumOut() == 2 && ft.Out(1) == errType {
			ops[t.Method(i).Name] = true
		}
	}
	return ops
})

// Middleware — strict-middleware операций тега staff (остальные теги проходят как есть):
//   - нулевой Handler — 501 http.not_implemented без вызова операции;
//   - в ctx — User-Agent и токен cookie запроса (strict-сервер их в объект запроса не кладёт): Meta
//     сценариев и oldToken принятия приглашения;
//   - ошибка операции — Problem; auth.ErrSessionInvalid (сессию погасили между конвейером и замком
//     сценария) стирает cookie, как конвейер (session.go), а auth.ErrSessionReplaced — нет: в браузере
//     уже новая cookie (гонка смены токена).
func (h *Handler) Middleware() oapi.StrictMiddlewareFunc {
	return func(f oapi.StrictHandlerFunc, operationID string) oapi.StrictHandlerFunc {
		if !operations()[operationID] {
			return f
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
			if h.app == nil {
				return nil, errNotImplemented
			}
			ctx = context.WithValue(ctx, requestKey{}, requestInfo{userAgent: r.UserAgent(), token: h.token(r)})
			resp, err := f(ctx, w, r, req)
			if err == nil {
				return resp, nil
			}
			if errors.Is(err, auth.ErrSessionInvalid) {
				http.SetCookie(w, h.clearCookie())
			}
			return nil, Problem(err)
		}
	}
}

// token — токен cookie сессии запроса ("" — cookie нет).
func (h *Handler) token(r *http.Request) string {
	c, err := r.Cookie(h.cookie.Name)
	if err != nil {
		return ""
	}
	return c.Value
}

type requestKey struct{}

// requestInfo — то, что strict-сервер не передаёт операции: User-Agent и токен cookie запроса.
type requestInfo struct {
	userAgent string
	token     string
}

func requestFrom(ctx context.Context) requestInfo {
	ri, _ := ctx.Value(requestKey{}).(requestInfo)
	return ri
}

// meta — откуда запрос: IP клиента (peer) и User-Agent как есть — в сессию и аудит сценариев.
func meta(ctx context.Context) staff.Meta {
	return staff.Meta{IP: peer.From(ctx).IP, UserAgent: requestFrom(ctx).userAgent}
}

// current — сотрудник запроса (конвейер, auth.WithStaff). Нет — auth.ErrSessionInvalid (401): операции с
// security без сессии до хендлера не доходят (валидатор), это защита в глубину.
func current(ctx context.Context) (*auth.Staff, error) {
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, auth.ErrSessionInvalid
	}
	return s, nil
}

// sessionCookie — cookie с новым токеном сессии (P8, спека §5.2): Path=/, HttpOnly, Secure по
// ADMIN_COOKIE_SECURE, SameSite=Strict; без Max-Age, Expires и Domain — cookie сессионная, сроки
// держит сервер.
func (h *Handler) sessionCookie(token string) *http.Cookie {
	// Secure — из ADMIN_COOKIE_SECURE: false только в dev по http (R6), как у конвейера
	return &http.Cookie{ //nolint:gosec // G124: Secure по конфигу, см. выше
		Name: h.cookie.Name, Value: token, Path: "/",
		HttpOnly: true, Secure: h.cookie.Secure, SameSite: http.SameSiteStrictMode,
	}
}

// clearCookie — стирание cookie сессии (Max-Age=0) с теми же атрибутами, что у установки.
func (h *Handler) clearCookie() *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure по конфигу, как у sessionCookie
		Name: h.cookie.Name, Path: "/", MaxAge: -1, // Max-Age=0: браузер удаляет сразу
		HttpOnly: true, Secure: h.cookie.Secure, SameSite: http.SameSiteStrictMode,
	}
}

// cookieResponse — ответ операции, которая ставит или стирает cookie сессии: http.SetCookie
// ДОБАВЛЯЕТ заголовок. Сгенерированные Visit…Response делают Header().Set("Set-Cookie") и затёрли бы
// стирание недействительной старой cookie, которое уже поставил конвейер; новая cookie должна идти
// последней — браузер применяет Set-Cookie по порядку. body nil — ответ без тела.
type cookieResponse struct {
	cookie *http.Cookie
	status int
	body   any
}

func (c cookieResponse) visit(w http.ResponseWriter) error {
	var buf bytes.Buffer
	if c.body != nil {
		if err := json.NewEncoder(&buf).Encode(c.body); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
	}
	http.SetCookie(w, c.cookie)
	w.WriteHeader(c.status)
	_, err := buf.WriteTo(w)
	return err
}

func (c cookieResponse) VisitStaffLoginResponse(w http.ResponseWriter) error { return c.visit(w) }
func (c cookieResponse) VisitStaffAcceptInviteResponse(w http.ResponseWriter) error {
	return c.visit(w)
}
func (c cookieResponse) VisitStaffVerifyMfaResponse(w http.ResponseWriter) error   { return c.visit(w) }
func (c cookieResponse) VisitStaffUnlockResponse(w http.ResponseWriter) error      { return c.visit(w) }
func (c cookieResponse) VisitStaffRecoveryAckResponse(w http.ResponseWriter) error { return c.visit(w) }
func (c cookieResponse) VisitStaffLogoutResponse(w http.ResponseWriter) error      { return c.visit(w) }

// withToken — ответ status с новым токеном сессии в cookie и телом body (nil — без тела).
func (h *Handler) withToken(token string, status int, body any) cookieResponse {
	return cookieResponse{cookie: h.sessionCookie(token), status: status, body: body}
}

// noStore — Cache-Control ответов с секретом (секрет TOTP, резервные коды; D14). Конвейер ставит его
// на все ответы, но сгенерированный Visit этих операций делает Header().Set("Cache-Control",
// Headers.CacheControl) — пустое значение стёрло бы no-store.
const noStore = "no-store"

// deref — строка необязательного поля тела ("" — поля нет).
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
