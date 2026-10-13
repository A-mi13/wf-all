package admin_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/staff"
)

// Коды модуля staff (staff.Codes) и коды staff.* в x-error-codes контракта admin совпадают в обе
// стороны (как TestGeoCodesDocumented): код без операции или операция с кодом вне списка — падение.
// Коды staff.* бывают и у операций платформы (approveApproval — отказы исполнителей подтверждений).
func TestStaffCodesDocumented(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			raw, ok := op.Extensions["x-error-codes"]
			if !ok {
				continue
			}
			b, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			if err := json.Unmarshal(b, &codes); err != nil {
				t.Fatalf("%s: x-error-codes — не список строк: %v", op.OperationID, err)
			}
			for _, c := range codes {
				if strings.HasPrefix(c, "staff.") {
					documented = append(documented, c)
				}
			}
		}
	}
	slices.Sort(documented)
	documented = slices.Compact(documented)
	want := slices.Sorted(slices.Values(staff.Codes))
	if !slices.Equal(documented, want) {
		t.Fatalf("коды staff в контракте %v, в staff.Codes %v", documented, want)
	}
}
