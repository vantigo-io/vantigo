package contracts_test

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// A due-orders read names customers, orders or every order — exactly one —
// within its bound and without naming one twice.
func TestDueOrdersRequest_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  contracts.DueOrdersRequest
		ok   bool
	}{
		{"customers", contracts.DueOrdersRequest{CustomerIDs: []int32{1, 2}}, true},
		{"orders", contracts.DueOrdersRequest{OrderIDs: []int64{1001}}, true},
		{"every", contracts.DueOrdersRequest{Every: true}, true},
		{"none", contracts.DueOrdersRequest{}, false},
		{"customers and orders", contracts.DueOrdersRequest{CustomerIDs: []int32{1}, OrderIDs: []int64{1}}, false},
		{"customers and every", contracts.DueOrdersRequest{CustomerIDs: []int32{1}, Every: true}, false},
		{"too many customers", contracts.DueOrdersRequest{CustomerIDs: make([]int32, contracts.MaxActualsRequests+1)}, false},
		{"a customer twice", contracts.DueOrdersRequest{CustomerIDs: []int32{3, 3}}, false},
		{"an order twice", contracts.DueOrdersRequest{OrderIDs: []int64{7, 8, 7}}, false},
		{"subscriptions", contracts.DueOrdersRequest{SubscriptionIDs: []int64{1001}}, true},
		{"orders and subscriptions", contracts.DueOrdersRequest{OrderIDs: []int64{1001}, SubscriptionIDs: []int64{1001}}, true},
		{"customers and subscriptions", contracts.DueOrdersRequest{CustomerIDs: []int32{1}, SubscriptionIDs: []int64{1}}, false},
		{"a subscription twice", contracts.DueOrdersRequest{SubscriptionIDs: []int64{4, 4}}, false},
		{"too many documents", contracts.DueOrdersRequest{OrderIDs: make([]int64, contracts.MaxBillableRows), SubscriptionIDs: []int64{1}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.req.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate() = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

// An allocate request names each line once with a positive quantity of at
// most three decimals.
func TestAllocateRequest_Validate(t *testing.T) {
	t.Parallel()
	line := func(id int64, q string) contracts.AllocateLine {
		return contracts.AllocateLine{LineID: id, Quantity: q}
	}
	cases := []struct {
		name  string
		lines []contracts.AllocateLine
		ok    bool
	}{
		{"one line", []contracts.AllocateLine{line(1, "2")}, true},
		{"three decimals", []contracts.AllocateLine{line(1, "0.125"), line(2, "999999999.999")}, true},
		{"no line", nil, false},
		{"zero", []contracts.AllocateLine{line(1, "0")}, false},
		{"negative", []contracts.AllocateLine{line(1, "-1")}, false},
		{"four decimals", []contracts.AllocateLine{line(1, "1.0005")}, false},
		{"not a decimal", []contracts.AllocateLine{line(1, "1e3")}, false},
		{"a fraction", []contracts.AllocateLine{line(1, "1/3")}, false},
		{"above numeric(12,3)", []contracts.AllocateLine{line(1, "1000000000")}, false},
		{"a line twice", []contracts.AllocateLine{line(1, "1"), line(1, "2")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := (contracts.AllocateRequest{Lines: tc.lines}).Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate() = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

// An allocate request may name subscriptions instead of lines, each once and
// each with a date to bill through.
func TestAllocateRequest_ValidateSubscriptions(t *testing.T) {
	t.Parallel()
	through := time.Date(2027, time.January, 31, 0, 0, 0, 0, time.UTC)
	sub := func(id int64, at time.Time) contracts.AllocateSubscription {
		return contracts.AllocateSubscription{SubscriptionID: id, Through: at}
	}
	cases := []struct {
		name string
		req  contracts.AllocateRequest
		ok   bool
	}{
		{"one subscription", contracts.AllocateRequest{Subscriptions: []contracts.AllocateSubscription{sub(1, through)}}, true},
		{"a line and a subscription", contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: 1, Quantity: "1"}},
			Subscriptions: []contracts.AllocateSubscription{sub(1, through)}}, true},
		{"no date", contracts.AllocateRequest{Subscriptions: []contracts.AllocateSubscription{sub(1, time.Time{})}}, false},
		{"a subscription twice", contracts.AllocateRequest{Subscriptions: []contracts.AllocateSubscription{sub(1, through), sub(1, through)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.req.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate() = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

// Allocations and ReleaseAllocations take 1 to MaxBillableRows ids, none twice.
func TestValidateAllocationIDs(t *testing.T) {
	t.Parallel()
	if err := contracts.ValidateAllocationIDs([]int64{1, 2}); err != nil {
		t.Errorf("two ids: %v", err)
	}
	if contracts.ValidateAllocationIDs(nil) == nil {
		t.Error("no id: want an error")
	}
	if contracts.ValidateAllocationIDs([]int64{4, 4}) == nil {
		t.Error("an id twice: want an error")
	}
	if contracts.ValidateAllocationIDs(make([]int64, contracts.MaxBillableRows+1)) == nil {
		t.Error("too many ids: want an error")
	}
}
