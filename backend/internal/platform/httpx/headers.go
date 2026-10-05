package httpx

import (
	"net/http"
	"strings"
)

// CombineHeaders сводит несколько строк заголовков names в одну через ", " — по RFC 9110 §5.3
// это равнозначно. Сгенерированный oapi-код принимает у заголовка-параметра ровно одну строку и
// на вторую ответил бы 400, а клиент или прокси вправе прислать список (Accept-Language)
// несколькими строками. Ставится до маршрута и валидатора.
func CombineHeaders(names ...string) func(http.Handler) http.Handler {
	keys := make([]string, 0, len(names))
	for _, n := range names {
		keys = append(keys, http.CanonicalHeaderKey(n))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, k := range keys {
				if vs := r.Header[k]; len(vs) > 1 {
					r.Header[k] = []string{strings.Join(vs, ", ")}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
