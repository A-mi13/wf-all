package httpx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// MaxBodyBytes — лимит тела запроса по умолчанию: JSON API больших тел не ждёт (спека §6.1).
const MaxBodyBytes = 1 << 20

// LimitBody ограничивает тело запроса n байтами. Чтение сверх лимита — *http.MaxBytesError;
// ValidateRequests отвечает на неё 413 request.too_large.
func LimitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// ValidateRequests проверяет запрос по контракту до strict-хендлера: strict-сервер
// oapi-codegen схему сам не валидирует (minLength, enum, format, обязательные заголовки).
// Маршрут не из контракта пропускается дальше — его ответит роутер (404/405).
// Аутентификация здесь не проверяется — это отдельный слой конвейера (спека §6.1).
func ValidateRequests(spec *openapi3.T) (func(http.Handler) http.Handler, error) {
	spec.Servers = nil // иначе FindRoute сверяет хост и префикс из servers
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("httpx: роутер контракта: %w", err)
	}
	// SkipSettingDefaults: валидатор только проверяет. Иначе kin-openapi дописывает default
	// в query и заголовки и перекодирует тело — хендлер получил бы не то, что прислал клиент.
	opts := &openapi3filter.Options{
		AuthenticationFunc:  openapi3filter.NoopAuthenticationFunc,
		MultiError:          true,
		SkipSettingDefaults: true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, params, err := router.FindRoute(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			var body []byte
			if r.Body != nil {
				if body, err = io.ReadAll(r.Body); err != nil {
					if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
						WriteProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "")
						return
					}
					WriteProblem(w, r, http.StatusBadRequest, "request.invalid", "")
					return
				}
				// тело читается здесь, чтобы отличить 413 от прочих ошибок чтения; kin-openapi
				// прочитает его ещё раз и сам вернёт в запрос для хендлера
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: opts}
			if verr := openapi3filter.ValidateRequest(r.Context(), in); verr != nil {
				if fields, ok := fieldErrors(verr); ok {
					WriteValidationProblem(w, r, fields)
					return
				}
				// текст ошибки kin-openapi клиенту не отдаём: он раскрывает устройство валидатора
				WriteProblem(w, r, http.StatusBadRequest, "request.invalid", "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// fieldErrors раскладывает ошибку kin-openapi по полям. false — ошибка не про поля
// (тело не разбирается, неверный Content-Type): это request.invalid.
func fieldErrors(err error) ([]FieldError, bool) {
	errs := []error{err}
	if me, ok := err.(openapi3.MultiError); ok { //nolint:errorlint // только верхний уровень: вложенные MultiError — внутри RequestError
		errs = me
	}
	var out []FieldError
	for _, e := range errs {
		re, ok := errors.AsType[*openapi3filter.RequestError](e)
		if !ok {
			return nil, false
		}
		if _, ok := errors.AsType[*openapi3filter.ParseError](re.Err); ok {
			return nil, false
		}
		prefix := "body"
		if re.Parameter != nil {
			prefix = re.Parameter.In + "." + re.Parameter.Name
		}
		if errors.Is(re.Err, openapi3filter.ErrInvalidRequired) {
			out = append(out, FieldError{Field: prefix, Code: "required"})
			continue
		}
		schemaErrs := schemaErrors(re.Err)
		if len(schemaErrs) == 0 {
			return nil, false
		}
		for _, se := range schemaErrs {
			out = append(out, FieldError{Field: fieldPath(prefix, se), Code: se.SchemaField})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Code < out[j].Code
	})
	return slices.Compact(out), true
}

func schemaErrors(err error) []*openapi3.SchemaError {
	if me, ok := err.(openapi3.MultiError); ok { //nolint:errorlint // MultiError — срез, раскрываем сами
		var out []*openapi3.SchemaError
		for _, e := range me {
			out = append(out, schemaErrors(e)...)
		}
		return out
	}
	if se, ok := errors.AsType[*openapi3.SchemaError](err); ok {
		return []*openapi3.SchemaError{se}
	}
	return nil
}

// fieldPath — путь поля через точку (body.items.0.name). У required имя пропавшего
// свойства kin-openapi сам кладёт в конец JSONPointer() (markSchemaErrorKey).
func fieldPath(prefix string, se *openapi3.SchemaError) string {
	ptr := se.JSONPointer()
	if len(ptr) == 0 {
		return prefix
	}
	return prefix + "." + strings.Join(ptr, ".")
}
