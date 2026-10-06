package invoices

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestConflictProblem_CarriesThePR1Fields: every extra field the pull
// request's 409s carry (invoices payments and reminders design D21, plan
// reading 47) — bank_file_duplicate's bankFileId, uploadedAt and uploadedBy;
// charge_payment_exceeds_outstanding's chargesOutstanding;
// collection_rates_outdated's kind and halfYear; bank_import_stale's
// lastBookedOn; payment_exceeds_open's invoiceId beside openAmount — goes
// through conflict(…) onto the wire under its name and back into the
// generated type unchanged, and none is on the wire when it is not set.
func TestConflictProblem_CarriesThePR1Fields(t *testing.T) {
	t.Parallel()
	bare, err := json.Marshal(conflict("payment_exceeds_open", "Cannot register", "Too much."))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var bareWire map[string]any
	if err := json.Unmarshal(bare, &bareWire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"bankFileId", "uploadedAt", "uploadedBy", "chargesOutstanding", "kind", "halfYear", "lastBookedOn", "invoiceId"} {
		if v, ok := bareWire[name]; ok {
			t.Errorf("a bare conflict carries %s = %v, want it absent", name, v)
		}
	}

	uploadedAt := time.Date(2026, 10, 6, 8, 30, 0, 0, time.UTC)
	uploadedBy := uuid.MustParse("6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11")
	problem := conflict("bank_file_duplicate", "Already imported", "This file was imported before.")
	problem.BankFileId = ptr(int64(1001))
	problem.UploadedAt = &uploadedAt
	problem.UploadedBy = &uploadedBy
	problem.ChargesOutstanding = ptr(35.5)
	problem.Kind = ptr("late_interest_percent")
	problem.HalfYear = ptr("2027-H1")
	problem.LastBookedOn = &openapi_types.Date{Time: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}
	problem.InvoiceId = ptr(int64(1002))
	problem.OpenAmount = ptr(100.25)

	body, err := json.Marshal(problem)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for name, want := range map[string]any{
		"code":               "bank_file_duplicate",
		"bankFileId":         float64(1001),
		"uploadedAt":         "2026-10-06T08:30:00Z",
		"uploadedBy":         "6f1c7a64-9c4f-4c43-9a51-2a8d4a1f0c11",
		"chargesOutstanding": 35.5,
		"kind":               "late_interest_percent",
		"halfYear":           "2027-H1",
		"lastBookedOn":       "2026-10-02",
		"invoiceId":          float64(1002),
		"openAmount":         100.25,
	} {
		if got := wire[name]; got != want {
			t.Errorf("the wire's %s = %#v, want %#v", name, got, want)
		}
	}

	var back = problem
	back.BankFileId, back.UploadedAt, back.UploadedBy, back.ChargesOutstanding = nil, nil, nil, nil
	back.Kind, back.HalfYear, back.LastBookedOn, back.InvoiceId, back.OpenAmount = nil, nil, nil, nil, nil
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal into the generated type: %v", err)
	}
	if !reflect.DeepEqual(back, problem) {
		t.Errorf("the problem back from the wire = %+v, want %+v", back, problem)
	}
}
