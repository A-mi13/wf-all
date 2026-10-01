package apitest

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Operation — операция контракта: имя метода strict-сервера и тег — модуль-реализатор.
type Operation struct{ Method, Tag string }

// Operations перечисляет операции спеки. Имя метода — operationId с заглавной буквы,
// как его называет oapi-codegen (operationId — lowerCamel, это проверяет contracts/).
func Operations(spec *openapi3.T) ([]Operation, error) {
	var out []Operation
	for path, item := range spec.Paths.Map() {
		for verb, op := range item.Operations() {
			if len(op.Tags) != 1 {
				return nil, fmt.Errorf("%s %s: нужен ровно один тег — имя модуля, есть %v", verb, path, op.Tags)
			}
			id := op.OperationID
			if id == "" {
				return nil, fmt.Errorf("%s %s: нет operationId", verb, path)
			}
			out = append(out, Operation{Method: strings.ToUpper(id[:1]) + id[1:], Tag: op.Tags[0]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })
	return out, nil
}

// Owners — какой модуль реализует каждый метод сервера: методы встроенных хендлеров
// принадлежат их модулю (moduleOf по пути пакета), объявленные на самом сервере — platform.
func Owners(server reflect.Type, moduleOf func(pkgPath string) string) map[string]string {
	owners := map[string]string{}
	for i := range server.NumField() {
		f := server.Field(i)
		if !f.Anonymous {
			continue
		}
		elem, ptr := f.Type, f.Type
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		} else {
			ptr = reflect.PointerTo(elem)
		}
		module := moduleOf(elem.PkgPath())
		for m := range ptr.NumMethod() {
			owners[ptr.Method(m).Name] = module
		}
	}
	ptr := reflect.PointerTo(server)
	for m := range ptr.NumMethod() {
		if name := ptr.Method(m).Name; owners[name] == "" {
			owners[name] = "platform"
		}
	}
	return owners
}

// ModuleOf — модуль по пути пакета хендлера wf/backend/internal/<модуль>/{httpapi,admin}.
func ModuleOf(pkgPath string) string {
	rest, ok := strings.CutPrefix(pkgPath, "wf/backend/internal/")
	if !ok {
		return ""
	}
	module, _, _ := strings.Cut(rest, "/")
	return module
}

// TagViolations — операции, которые реализует не тот модуль, что указан в теге.
func TagViolations(ops []Operation, owners map[string]string) []string {
	var out []string
	for _, op := range ops {
		owner, ok := owners[op.Method]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: сервер не реализует операцию", op.Method))
		case owner != op.Tag:
			out = append(out, fmt.Sprintf("%s: тег %q, а реализует модуль %q", op.Method, op.Tag, owner))
		}
	}
	return out
}
