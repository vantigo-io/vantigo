package customers

import (
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestMergeSummary_NamesWhatMovedInWords pins customer.merged's summary
// (customers merge design D3): the absorbed customer by number and name, then
// every kind that moved anything, in the answer's order, singular where there
// is one — and a kind this module has no word for still reads, by its name.
func TestMergeSummary_NamesWhatMovedInWords(t *testing.T) {
	absorbed := mergedCustomerSnapshot{CustomerNumber: 1005, Name: "Acme Norge AS"}
	moved := []contracts.RepointedReferences{
		{Kind: mergeKindContacts, Count: 3}, {Kind: mergeKindAddresses, Count: 2},
		{Kind: mergeKindTimelineEntries, Count: 14}, {Kind: mergeKindTags, Count: 0},
		{Kind: "projects.projects", Count: 2}, {Kind: "energy.supplyPeriods", Count: 1},
		{Kind: "somewhere.else", Count: 4},
	}
	want := "Absorbed #1005 Acme Norge AS: 3 contacts, 2 addresses, 14 timeline entries, 2 projects, 1 supply period, 4 × somewhere.else"
	if got := mergeSummary(absorbed, moved); got != want {
		t.Errorf("mergeSummary =\n%q\nwant\n%q", got, want)
	}
	nothing := []contracts.RepointedReferences{{Kind: mergeKindContacts, Count: 0}}
	if got := mergeSummary(absorbed, nothing); got != "Absorbed #1005 Acme Norge AS" {
		t.Errorf("mergeSummary with nothing moved = %q, want the absorbed customer alone", got)
	}
}

// TestMergeLockOrder_IsAscendingAndLocksOneRowOnce: every writer that locks
// several customer rows takes them in ascending id order (the contact
// delete's rule), which is what keeps two merges of one pair from
// deadlocking; and a merge of a customer into itself locks its one row once,
// so the ladder can answer merge_self rather than wait on itself.
func TestMergeLockOrder_IsAscendingAndLocksOneRowOnce(t *testing.T) {
	for _, tc := range []struct {
		a, b int32
		want []int32
	}{
		{1009, 1002, []int32{1002, 1009}},
		{1002, 1009, []int32{1002, 1009}},
		{1004, 1004, []int32{1004}},
	} {
		if got := mergeLockOrder(tc.a, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("mergeLockOrder(%d, %d) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
