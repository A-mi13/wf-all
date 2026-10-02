package ratelimit

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// ClassPolicy — политики класса ручек по ключам; нулевая — ключ не ограничивается.
type ClassPolicy struct {
	IP, User, Device Policy
}

// Rules — политики по классам: класс операции — x-rate-limit контракта, без него — default.
type Rules map[string]ClassPolicy

const DefaultClass = "default"

// DefaultRules — стартовые политики. IP щедрее пользователя: за одним адресом мобильного
// оператора (CGNAT) — многие. auth — вход, регистрация, коды: перебор паролей и рассылки.
func DefaultRules() Rules {
	return Rules{
		DefaultClass: {
			IP:     Policy{Limit: 600, Period: time.Minute, Burst: 120},
			User:   Policy{Limit: 300, Period: time.Minute, Burst: 60},
			Device: Policy{Limit: 300, Period: time.Minute, Burst: 60},
		},
		"auth": {
			IP:     Policy{Limit: 60, Period: time.Minute, Burst: 20},
			Device: Policy{Limit: 20, Period: time.Minute, Burst: 10},
		},
	}
}

var (
	classRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	ruleRe  = regexp.MustCompile(`^(\d+)/([0-9a-z]+)(?::(\d+))?$`)
)

// ParseRules — переопределения из окружения поверх base, через запятую:
// <класс>.<ip|user|device>=<лимит>/<период>[:<всплеск>] или =off. Пример:
// "auth.ip=30/1m:10,default.device=off". base не меняется.
func ParseRules(s string, base Rules) (Rules, error) {
	out := maps.Clone(base)
	if out == nil {
		out = Rules{}
	}
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, value, ok := strings.Cut(item, "=")
		class, key, ok2 := strings.Cut(name, ".")
		if !ok || !ok2 || !classRe.MatchString(class) {
			return nil, fmt.Errorf("ratelimit: %q — нужен <класс>.<ключ>=<лимит>/<период>[:<всплеск>]", item)
		}
		var p Policy
		if value != "off" {
			m := ruleRe.FindStringSubmatch(value)
			if m == nil {
				return nil, fmt.Errorf("ratelimit: %q — значение не <лимит>/<период>[:<всплеск>] и не off", item)
			}
			var err error
			if p.Limit, err = strconv.Atoi(m[1]); err != nil {
				return nil, fmt.Errorf("ratelimit: %q — лимит: %w", item, err)
			}
			if p.Period, err = time.ParseDuration(m[2]); err != nil {
				return nil, fmt.Errorf("ratelimit: %q — период: %w", item, err)
			}
			if m[3] != "" {
				if p.Burst, err = strconv.Atoi(m[3]); err != nil {
					return nil, fmt.Errorf("ratelimit: %q — всплеск: %w", item, err)
				}
			}
			if p.Limit == 0 {
				return nil, fmt.Errorf("ratelimit: %q — лимит 0; выключить ключ — off", item)
			}
			if err := p.Validate(); err != nil {
				return nil, fmt.Errorf("ratelimit: %q: %w", item, err)
			}
		}
		cp := out[class]
		switch key {
		case "ip":
			cp.IP = p
		case "user":
			cp.User = p
		case "device":
			cp.Device = p
		default:
			return nil, fmt.Errorf("ratelimit: %q — ключ %q, нужен ip, user или device", item, key)
		}
		out[class] = cp
	}
	return out, nil
}

// UnknownClasses — операции, чей x-rate-limit не описан в правилах: «<МЕТОД> <путь>: x-rate-limit <класс>».
func UnknownClasses(spec *openapi3.T, r Rules) []string {
	var out []string
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			class, _ := op.Extensions["x-rate-limit"].(string)
			if class == "" {
				continue
			}
			if _, ok := r[class]; !ok {
				out = append(out, fmt.Sprintf("%s %s: x-rate-limit %s", method, path, class))
			}
		}
	}
	slices.Sort(out)
	return out
}
