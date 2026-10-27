package apitest

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
)

// CommonCodeViolations сверяет x-error-codes-common контракта с кодами платформы
// (httpx.PlatformCodes) в обе стороны: код, который платформа выдаёт, задокументирован,
// а в контракте нет кода, которого платформа не выдаёт.
func CommonCodeViolations(spec *openapi3.T, platform []string) ([]string, error) {
	raw, ok := spec.Extensions["x-error-codes-common"]
	if !ok {
		return nil, fmt.Errorf("в контракте нет x-error-codes-common")
	}
	// значение расширения — json.RawMessage или уже разобранный []any, в зависимости от загрузчика
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("x-error-codes-common: %w", err)
	}
	var common []string
	if err := json.Unmarshal(b, &common); err != nil {
		return nil, fmt.Errorf("x-error-codes-common — не список строк: %w", err)
	}
	var out []string
	for _, c := range platform {
		if !slices.Contains(common, c) {
			out = append(out, fmt.Sprintf("код платформы %s не перечислен в x-error-codes-common контракта", c))
		}
	}
	for _, c := range common {
		if !slices.Contains(platform, c) {
			out = append(out, fmt.Sprintf("x-error-codes-common: %s — платформа такой код не выдаёт (нет в httpx.PlatformCodes)", c))
		}
	}
	return out, nil
}

// OperationCodes — x-error-codes каждой операции контракта по operationId (в спеке, вшитой
// oapi-codegen, он с заглавной буквы — как имя метода): сверка кодов модуля
// и платформы с контрактом. Нет расширения или оно не список строк — ошибка, а не пустой список.
func OperationCodes(spec *openapi3.T) (map[string][]string, error) {
	out := map[string][]string{}
	for path, item := range spec.Paths.Map() {
		for verb, op := range item.Operations() {
			raw, ok := op.Extensions["x-error-codes"]
			if !ok {
				return nil, fmt.Errorf("%s %s: нет x-error-codes", verb, path)
			}
			b, err := json.Marshal(raw)
			if err != nil {
				return nil, fmt.Errorf("%s %s: x-error-codes: %w", verb, path, err)
			}
			codes := []string{}
			if err := json.Unmarshal(b, &codes); err != nil {
				return nil, fmt.Errorf("%s %s: x-error-codes — не список строк: %w", verb, path, err)
			}
			out[op.OperationID] = codes
		}
	}
	return out, nil
}
