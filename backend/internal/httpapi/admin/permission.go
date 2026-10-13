package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// paramCity — параметр пути с городом объекта: право scoped сверяется с городами модератора.
const paramCity = "cityId"

type permKind uint8

const (
	permAnonymous permKind = iota // без сессии
	permSession                   // сессия любой стадии; стадию проверяет сценарий
	permSelf                      // активная сессия, своё
	permKey                       // ключ каталога
	permHandler                   // хоть одно из x-permission-any; точно решает сценарий
)

// permRule — x-permission операции, разобранное на старте.
type permRule struct {
	kind  permKind
	key   string   // permKey
	anyOf []string // permHandler
}

// permRules — правила по операциям контракта (указатель — тот же, что в httpx.Route).
type permRules map[*openapi3.Operation]permRule

// buildPermissions — правила x-permission всех операций контракта (спека сотрудников §2.4, P9).
// Операция без x-permission, нестроковое значение, ключ вне каталога или черновой, handler без
// x-permission-any (или с негодным ключом в нём), x-permission-any не у handler — ошибка старта.
func buildPermissions(spec *openapi3.T) (permRules, error) {
	rules := permRules{}
	var errs []string
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			rule, err := permissionRule(op)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s %s: %v", method, path, err))
				continue
			}
			rules[op] = rule
		}
	}
	if len(errs) > 0 {
		slices.Sort(errs)
		return nil, fmt.Errorf("admin: x-permission контракта: %v", errs)
	}
	return rules, nil
}

func permissionRule(op *openapi3.Operation) (permRule, error) {
	raw, ok := op.Extensions["x-permission"]
	if !ok {
		return permRule{}, errors.New("нет x-permission")
	}
	value, _ := raw.(string)
	if value == "" {
		return permRule{}, fmt.Errorf("x-permission %v — не непустая строка", raw)
	}
	rawAny, hasAny := op.Extensions["x-permission-any"]
	if hasAny != (value == perm.Handler) {
		return permRule{}, errors.New("x-permission-any — ровно у x-permission: handler")
	}
	switch value {
	case perm.Anonymous:
		return permRule{kind: permAnonymous}, nil
	case perm.Session:
		return permRule{kind: permSession}, nil
	case perm.Self:
		return permRule{kind: permSelf}, nil
	case perm.Handler:
		list, _ := rawAny.([]any)
		if len(list) == 0 {
			return permRule{}, fmt.Errorf("x-permission-any %v — не непустой список", rawAny)
		}
		rule := permRule{kind: permHandler}
		for _, v := range list {
			key, _ := v.(string)
			if err := catalogKey(key); err != nil {
				return permRule{}, fmt.Errorf("x-permission-any: %w", err)
			}
			rule.anyOf = append(rule.anyOf, key)
		}
		return rule, nil
	default:
		if err := catalogKey(value); err != nil {
			return permRule{}, fmt.Errorf("x-permission: %w", err)
		}
		return permRule{kind: permKey, key: value}, nil
	}
}

// catalogKey — ключ — действующее право каталога: черновое в правах ролей не бывает, и такая
// операция была бы закрыта для всех.
func catalogKey(key string) error {
	p, ok := perm.Lookup(key)
	switch {
	case !ok:
		return fmt.Errorf("право %q не из каталога perm", key)
	case p.Draft:
		return fmt.Errorf("право %q черновое", key)
	}
	return nil
}

// permissions — страж x-permission (спека сотрудников §2.4, §5.1). После валидатора (вход по
// security он уже проверил), до rate limit. Маршрут контракта без правила — 500 и запись в лог
// (fail-closed, P9). anonymous — пропуск; session — любая стадия; self, ключ и handler — только
// активная стадия: mfa_pending → 401 auth.unauthenticated, setup → 403 auth.setup_required,
// Reauth → 401 auth.reauth_required. Ключ: {cityId} в пути — Decide(ключ, город) не Deny (Dual —
// пропуск: сценарий создаст запрос на подтверждение); без города — Grant не Deny (scoped-список
// фильтрует ручка). handler — хоть одно право из x-permission-any не Deny. Нет права — 403
// auth.forbidden.
func permissions(rules permRules, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt, ok := httpx.RouteFrom(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			rule, ok := rules[rt.Operation()]
			if !ok {
				log.ErrorContext(r.Context(), "admin: у операции контракта нет правила x-permission",
					"operation", rt.Operation().OperationID)
				httpx.WriteProblem(w, r, http.StatusInternalServerError, httpx.CodeInternal, "")
				return
			}
			if rule.kind == permAnonymous {
				next.ServeHTTP(w, r)
				return
			}
			s, ok := auth.StaffFrom(r.Context())
			if !ok {
				// валидатор отвечает 401 раньше на операциях с security; сюда попадает только
				// операция без security с правом не anonymous — тоже без входа не пускаем
				httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthenticated, "")
				return
			}
			if rule.kind == permSession {
				next.ServeHTTP(w, r)
				return
			}
			switch {
			case s.Stage == auth.StageSetup:
				httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeSetupRequired, "")
				return
			case s.Stage != auth.StageActive: // mfa_pending и незнакомая стадия
				httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthenticated, "")
				return
			case s.Reauth:
				httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeReauthRequired, "")
				return
			}
			if !allowed(rule, s, rt) {
				httpx.WriteProblem(w, r, http.StatusForbidden, httpx.CodeForbidden, "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func allowed(rule permRule, s *auth.Staff, rt *httpx.Route) bool {
	switch rule.kind {
	case permSelf:
		return true
	case permKey:
		if v, ok := rt.Param(paramCity); ok {
			city, err := uuid.Parse(v)
			return err == nil && s.Decide(rule.key, city) != auth.Deny
		}
		// без города Decide(ключ, uuid.Nil) дал бы Deny и scoped-модератору (⚠️ 3a)
		return s.Grant(rule.key) != auth.GrantDeny
	case permHandler:
		return slices.ContainsFunc(rule.anyOf, func(k string) bool { return s.Grant(k) != auth.GrantDeny })
	}
	return false
}
