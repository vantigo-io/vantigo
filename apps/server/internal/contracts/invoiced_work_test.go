package contracts_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// A billable read names either projects or rows, never both and never
// neither, within its bound and without naming one twice: a provider builds
// one query per request and refuses one of unbounded size or ambiguous shape.
func TestBillableRequest_Validate(t *testing.T) {
	t.Parallel()
	projects := func(n int) []int32 {
		out := make([]int32, n)
		for i := range out {
			out[i] = int32(i + 1)
		}
		return out
	}
	ids := func(n int) []int64 {
		out := make([]int64, n)
		for i := range out {
			out[i] = int64(i + 1)
		}
		return out
	}
	cases := []struct {
		name string
		req  contracts.BillableRequest
		ok   bool
	}{
		{"projects", contracts.BillableRequest{ProjectIDs: []int32{1, 2}}, true},
		{"ids", contracts.BillableRequest{IDs: []int64{7, 3}}, true},
		{"projects at the bound", contracts.BillableRequest{ProjectIDs: projects(contracts.MaxActualsRequests)}, true},
		{"ids at the bound", contracts.BillableRequest{IDs: ids(contracts.MaxBillableRows)}, true},
		{"neither", contracts.BillableRequest{}, false},
		{"neither, empty slices", contracts.BillableRequest{ProjectIDs: []int32{}, IDs: []int64{}}, false},
		{"both", contracts.BillableRequest{ProjectIDs: []int32{1}, IDs: []int64{1}}, false},
		{"too many projects", contracts.BillableRequest{ProjectIDs: projects(contracts.MaxActualsRequests + 1)}, false},
		{"too many ids", contracts.BillableRequest{IDs: ids(contracts.MaxBillableRows + 1)}, false},
		{"a project twice", contracts.BillableRequest{ProjectIDs: []int32{4, 5, 4}}, false},
		{"an id twice", contracts.BillableRequest{IDs: []int64{9, 8, 9}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.req.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !tc.ok && err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}

// A holder answers a source it will not stamp as *WorkSourceRefusal, and the
// issue tells it from a failure by errors.As — through any wrapping the holder
// adds — so the refusal must be an error that names its code and source.
func TestWorkSourceRefusal_IsAnError(t *testing.T) {
	t.Parallel()
	refusal := &contracts.WorkSourceRefusal{
		Source: contracts.WorkSource{Kind: contracts.WorkSourceHours, ID: 42},
		Code:   contracts.SourceChanged,
		Detail: "the entry's revision moved",
	}
	err := fmt.Errorf("time: mark invoiced: %w", refusal)

	var got *contracts.WorkSourceRefusal
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(%v) found no *WorkSourceRefusal", err)
	}
	if got != refusal {
		t.Errorf("errors.As found %v, want the refusal itself", got)
	}
	for _, want := range []string{"source_changed", "time.entry", "42"} {
		if msg := refusal.Error(); !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want it to name %q", msg, want)
		}
	}
	if errors.As(errors.New("connection reset"), &got) {
		t.Error("errors.As matched a plain error as a refusal")
	}
}

// The lock order is data the issue walks: Projects, then Expenses, then Time.
func TestInvoicedWorkOrder_IsProjectsExpensesTime(t *testing.T) {
	t.Parallel()
	want := []contracts.WorkSourceKind{"projects.milestone", "expenses.entry", "time.entry"}
	if len(contracts.InvoicedWorkOrder) != len(want) {
		t.Fatalf("InvoicedWorkOrder = %v, want %v", contracts.InvoicedWorkOrder, want)
	}
	for i := range want {
		if contracts.InvoicedWorkOrder[i] != want[i] {
			t.Fatalf("InvoicedWorkOrder = %v, want %v", contracts.InvoicedWorkOrder, want)
		}
	}
}
