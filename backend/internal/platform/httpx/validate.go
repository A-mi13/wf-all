package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// Форматы, которые kin-openapi по умолчанию не проверяет (из коробки — byte, date, date-time,
// int32, int64): без них uuid/email из контракта проходили бы валидатор, а падали бы в биндинге
// oapi-codegen. Регистрация глобальная: в kin-openapi v0.149 валидаторы уровня документа или
// Options.SchemaValidationOptions доходят только до тела, ValidateParameter их не передаёт.
// Запись в карту — только здесь, в init; дальше она лишь читается.
func init() {
	openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC9562))
	openapi3.DefineStringFormatValidator("email", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForEmail))
}

// MaxBodyBytes — лимит тела запроса по умолчанию: JSON API больших тел не ждёт (спека §6.1).
const MaxBodyBytes = 1 << 20

// LimitBody ограничивает тело запроса n байтами. Чтение сверх лимита — *http.MaxBytesError;
// ValidateRequests отвечает на неё 413 request.too_large.
func LimitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// nil — запрос собран руками (сервер всегда даёт хотя бы http.NoBody):
			// MaxBytesReader поверх nil паникует при чтении
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ValidateOptions — настройки проверки запроса.
type ValidateOptions struct {
	// Authenticated — есть ли в ctx аутентифицированный пользователь (Principal кладёт слой
	// аутентификации раньше, спека §6.1). nil — никто не вошёл: операции с security — 401.
	Authenticated func(context.Context) bool
	// WWWAuthenticate — схема в заголовке ответа 401 (RFC 9110 §11.6.1), например "Bearer".
	WWWAuthenticate string
}

var errUnauthenticated = errors.New("httpx: нет аутентификации")

// ValidateRequests проверяет запрос по контракту до strict-хендлера: strict-сервер
// oapi-codegen схему сам не валидирует (minLength, enum, format, обязательные заголовки).
// Маршрут берётся из ctx (Routes); запрос без маршрута пропускается — его ответит роутер.
// Требование входа — из security операции: нет Principal — 401 auth.unauthenticated раньше
// нарушений схемы.
func ValidateRequests(o ValidateOptions) func(http.Handler) http.Handler {
	// SkipSettingDefaults: валидатор только проверяет. Иначе kin-openapi дописывает default
	// в query и заголовки и перекодирует тело — хендлер получил бы не то, что прислал клиент.
	opts := &openapi3filter.Options{
		AuthenticationFunc: func(ctx context.Context, _ *openapi3filter.AuthenticationInput) error {
			if o.Authenticated != nil && o.Authenticated(ctx) {
				return nil
			}
			return errUnauthenticated
		},
		MultiError:          true,
		SkipSettingDefaults: true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt, ok := RouteFrom(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			var body []byte
			if r.Body != nil {
				var err error
				if body, err = io.ReadAll(r.Body); err != nil {
					if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
						WriteProblem(w, r, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "")
						return
					}
					WriteProblem(w, r, http.StatusBadRequest, CodeRequestInvalid, "")
					return
				}
				// тело читается здесь, чтобы отличить 413 от прочих ошибок чтения; kin-openapi
				// прочитает его ещё раз и сам вернёт в запрос для хендлера
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			in := &openapi3filter.RequestValidationInput{Request: r, PathParams: rt.params, Route: rt.route, Options: opts}
			if verr := openapi3filter.ValidateRequest(r.Context(), in); verr != nil {
				if _, unauth := errors.AsType[*openapi3filter.SecurityRequirementsError](verr); unauth {
					if o.WWWAuthenticate != "" {
						w.Header().Set("WWW-Authenticate", o.WWWAuthenticate)
					}
					WriteProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "")
					return
				}
				if fields, ok := fieldErrors(verr); ok {
					WriteValidationProblem(w, r, fields)
					return
				}
				// текст ошибки kin-openapi клиенту не отдаём: он раскрывает устройство валидатора
				WriteProblem(w, r, http.StatusBadRequest, CodeRequestInvalid, "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
		prefix := "body"
		if re.Parameter != nil {
			prefix = re.Parameter.In + "." + re.Parameter.Name
		}
		_, parseErr := errors.AsType[*openapi3filter.ParseError](re.Err)
		switch {
		case parseErr && re.Parameter == nil:
			return nil, false // тело не разбирается как JSON — это не про поле
		case parseErr:
			// параметр не приводится к типу схемы (limit=abc) — как type в теле
			out = append(out, FieldError{Field: prefix, Code: "type"})
			continue
		case errors.Is(re.Err, openapi3filter.ErrInvalidEmptyValue):
			// параметр без значения (?limit=), а allowEmptyValue в контракте нет
			out = append(out, FieldError{Field: prefix, Code: "empty"})
			continue
		case errors.Is(re.Err, openapi3filter.ErrInvalidRequired):
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
