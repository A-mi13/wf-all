package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"wf/backend/internal/platform/httpx"
)

// headerAdmin — заголовок страницы админки на изменяющих запросах (контракт: AdminHeader).
const headerAdmin = "X-WF-Admin"

// csrf — защита изменяющих запросов от подделки с чужого сайта (спека сотрудников §5.1): любой
// метод, кроме GET, HEAD, OPTIONS и TRACE, — ровно один Origin, равный ADMIN_ORIGIN, и ровно один
// X-WF-Admin: 1, иначе 403 auth.csrf_failed. Стоит до валидатора: тело не разбирается, пока
// запрос не признан своим; анонимные мутации (вход, приглашение) проверяются так же. Чужой
// сайт не выставит ни свой Origin админки, ни нестандартный заголовок без CORS-разрешения,
// которого у admin-api нет; double-submit не нужен (спека бэкенда §10).
func csrf(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if safeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			o, a := r.Header.Values("Origin"), r.Header.Values(headerAdmin)
			if len(o) != 1 || o[0] != origin || len(a) != 1 || a[0] != "1" {
				httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeCSRFFailed, "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// ValidateOrigin — ADMIN_ORIGIN в той форме, в какой браузер шлёт заголовок Origin (RFC 6454):
// схема http или https, хост в нижнем регистре и ASCII (IDN — A-label xn--…), порт — только
// нестандартный; без пути, слэша в конце, запроса, фрагмента и userinfo. Иначе CSRF не совпал бы ни с
// одним запросом — отказ старта, а не админка, где не работает ни одна кнопка.
func ValidateOrigin(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("admin: ADMIN_ORIGIN %q: %w", s, err)
	}
	var errs []error
	if u.Scheme != "http" && u.Scheme != "https" {
		errs = append(errs, errors.New("схема — http или https"))
	}
	if u.Host == "" || u.Hostname() == "" {
		errs = append(errs, errors.New("нет хоста"))
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		errs = append(errs, errors.New("только схема, хост и порт — без пути, «/» в конце, запроса и фрагмента"))
	}
	if s != strings.ToLower(s) {
		errs = append(errs, errors.New("в нижнем регистре — так его шлёт браузер"))
	}
	// IDN-домен браузер пишет в Origin в форме A-label (Punycode): Unicode-хост (и %-кодированный — url.Parse
	// раскодирует его в Unicode; %-код ASCII-байта url.Parse отвергает сам) не совпал бы ни с одним
	// запросом — все мутации получили бы 403 auth.csrf_failed
	if !ascii(u.Hostname()) {
		errs = append(errs, errors.New("хост — в форме A-label (xn--…), как его шлёт браузер: например, https://xn--80aqifr.xn--p1ai, а не Unicode"))
	}
	if port := u.Port(); (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		errs = append(errs, errors.New("стандартный порт браузер в Origin не пишет — уберите его"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("admin: ADMIN_ORIGIN %q: %w", s, errors.Join(errs...))
	}
	return nil
}

// ValidateCookieSecure — ADMIN_COOKIE_SECURE=false только с http-origin (dev, R6): на https cookie сессии без
// Secure ушла бы и по http, а без префикса __Host- её подменили бы с поддомена. Проверка на старте, до базы
// (как ValidateOrigin); NewHandler проверяет её же.
func ValidateCookieSecure(origin string, secure bool) error {
	if strings.HasPrefix(origin, "https://") && !secure {
		return fmt.Errorf("admin: ADMIN_ORIGIN %q — https, а ADMIN_COOKIE_SECURE=false: cookie сессии без Secure — только для dev по http", origin)
	}
	return nil
}

// ascii — только печатные символы ASCII.
func ascii(s string) bool {
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
