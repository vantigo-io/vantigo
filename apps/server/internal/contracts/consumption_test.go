package contracts_test

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The consumption contract's requests (energy consumption billing design
// D1): a due-supplies read names customers, a settle names customers or
// supply periods but never both, a settlements read names ids — each within
// its bound, none twice — and the first two a through.
func TestConsumptionRequests_Validate(t *testing.T) {
	t.Parallel()
	through := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	int32s := func(n int) []int32 {
		out := make([]int32, n)
		for i := range out {
			out[i] = int32(i + 1)
		}
		return out
	}
	int64s := func(n int) []int64 {
		out := make([]int64, n)
		for i := range out {
			out[i] = int64(i + 1)
		}
		return out
	}
	cases := []struct {
		name string
		err  error
		ok   bool
	}{
		{"due: customers", contracts.DueSuppliesRequest{CustomerIDs: []int32{3, 1}, Through: through}.Validate(), true},
		{"due: customers at the bound", contracts.DueSuppliesRequest{CustomerIDs: int32s(contracts.MaxActualsRequests), Through: through}.Validate(), true},
		{"due: no customer", contracts.DueSuppliesRequest{Through: through}.Validate(), false},
		{"due: too many customers", contracts.DueSuppliesRequest{CustomerIDs: int32s(contracts.MaxActualsRequests + 1), Through: through}.Validate(), false},
		{"due: a customer twice", contracts.DueSuppliesRequest{CustomerIDs: []int32{4, 4}, Through: through}.Validate(), false},
		{"due: no through", contracts.DueSuppliesRequest{CustomerIDs: []int32{4}}.Validate(), false},

		{"settle: customers", contracts.SettleRequest{CustomerIDs: []int32{1}, Through: through}.Validate(), true},
		{"settle: supply periods", contracts.SettleRequest{SupplyPeriodIDs: []int32{1001, 1002}, Through: through}.Validate(), true},
		{"settle: supply periods at the bound", contracts.SettleRequest{SupplyPeriodIDs: int32s(contracts.MaxBillableRows), Through: through}.Validate(), true},
		{"settle: neither", contracts.SettleRequest{Through: through}.Validate(), false},
		{"settle: both", contracts.SettleRequest{CustomerIDs: []int32{1}, SupplyPeriodIDs: []int32{1001}, Through: through}.Validate(), false},
		{"settle: too many customers", contracts.SettleRequest{CustomerIDs: int32s(contracts.MaxActualsRequests + 1), Through: through}.Validate(), false},
		{"settle: too many supply periods", contracts.SettleRequest{SupplyPeriodIDs: int32s(contracts.MaxBillableRows + 1), Through: through}.Validate(), false},
		{"settle: a customer twice", contracts.SettleRequest{CustomerIDs: []int32{2, 2}, Through: through}.Validate(), false},
		{"settle: a supply period twice", contracts.SettleRequest{SupplyPeriodIDs: []int32{9, 8, 9}, Through: through}.Validate(), false},
		{"settle: no through", contracts.SettleRequest{CustomerIDs: []int32{1}}.Validate(), false},

		{"ids", contracts.ValidateSettlementIDs([]int64{7, 3}), true},
		{"ids at the bound", contracts.ValidateSettlementIDs(int64s(contracts.MaxBillableRows)), true},
		{"no id", contracts.ValidateSettlementIDs(nil), false},
		{"too many ids", contracts.ValidateSettlementIDs(int64s(contracts.MaxBillableRows + 1)), false},
		{"an id twice", contracts.ValidateSettlementIDs([]int64{5, 5}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.ok && tc.err != nil {
				t.Errorf("Validate() = %v, want nil", tc.err)
			}
			if !tc.ok && tc.err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}
