package inventory

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestJudgeLow is spec §10's Placement table for the rule (I56) and the
// rounding (I57): low at =, the target without a maximum, rounding with and
// without packagings and its tie-break, past the maximum, and a reorder
// point of 0 (§9 edge cases 12 and 13).
func TestJudgeLow(t *testing.T) {
	t.Parallel()
	box := contracts.PackagingEntry{ID: 7, Name: "Eske", Kind: "case", Quantity: "15.000", SortOrder: 1}
	pallet := contracts.PackagingEntry{ID: 8, Name: "Pall", Kind: "pallet", Quantity: "600.000", SortOrder: 0}
	r := func(s string) *string { return &s }
	for _, tc := range []struct {
		name                            string
		onHand, reserved, incoming      string
		reorderPoint, maximum           *string
		packagings                      []contracts.PackagingEntry
		low                             bool
		needed, suggested, aboveMaximum string
		packagingID                     int32
		count                           int64
	}{
		{name: "no reorder point is never low", onHand: "0", reorderPoint: nil, maximum: r("10")},
		{name: "above the reorder point", onHand: "11", reorderPoint: r("10")},
		{name: "low at =", onHand: "10", reorderPoint: r("10"), low: true, needed: "1", suggested: "1"},
		{name: "reserved and incoming count", onHand: "12", reserved: "5", incoming: "2", reorderPoint: r("10"), maximum: r("20"),
			low: true, needed: "11", suggested: "11"},
		{name: "incoming lifts it off", onHand: "8", incoming: "3", reorderPoint: r("10")},
		{name: "the target without a maximum is one above", onHand: "4", reorderPoint: r("10"), low: true, needed: "7", suggested: "7"},
		{name: "a maximum equal to the reorder point still needs 1", onHand: "10", reorderPoint: r("10"), maximum: r("10"),
			low: true, needed: "1", suggested: "1", aboveMaximum: "1"},
		{name: "a fraction short is rounded up to 1", onHand: "9.5", reorderPoint: r("10"), maximum: r("10"),
			low: true, needed: "1", suggested: "1", aboveMaximum: "0.5"},
		{name: "rounded up to whole boxes", onHand: "19", reorderPoint: r("20"), maximum: r("40"), packagings: []contracts.PackagingEntry{box},
			low: true, needed: "21", suggested: "30", packagingID: 7, count: 2, aboveMaximum: "9"},
		{name: "a whole number of boxes is not rounded", onHand: "10", reorderPoint: r("20"), maximum: r("40"), packagings: []contracts.PackagingEntry{box},
			low: true, needed: "30", suggested: "30", packagingID: 7, count: 2},
		{name: "the smallest packaging, whatever its sort order", onHand: "0", reorderPoint: r("5"), maximum: r("10"),
			packagings: []contracts.PackagingEntry{pallet, box}, low: true, needed: "10", suggested: "15", packagingID: 7, count: 1, aboveMaximum: "5"},
		{name: "a tie goes to the lower sort order", onHand: "0", reorderPoint: r("5"), maximum: r("10"),
			packagings: []contracts.PackagingEntry{{ID: 3, Quantity: "15.000", SortOrder: 2}, {ID: 9, Quantity: "15.000", SortOrder: 1}},
			low:        true, needed: "10", suggested: "15", packagingID: 9, count: 1, aboveMaximum: "5"},
		{name: "then to the lower id", onHand: "0", reorderPoint: r("5"), maximum: r("10"),
			packagings: []contracts.PackagingEntry{{ID: 9, Quantity: "15.000", SortOrder: 1}, {ID: 3, Quantity: "15.000", SortOrder: 1}},
			low:        true, needed: "10", suggested: "15", packagingID: 3, count: 1, aboveMaximum: "5"},
		{name: "reorder point 0 is low only at 0 or below (§9 13)", onHand: "1", reorderPoint: r("0")},
		{name: "reorder point 0, out of stock: the target is 1", onHand: "0", reorderPoint: r("0"), low: true, needed: "1", suggested: "1"},
		{name: "a negative available needs more", onHand: "2", reserved: "5", reorderPoint: r("0"), maximum: r("4"),
			low: true, needed: "7", suggested: "7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := lowLevels{onHand: ratFromText(tc.onHand), reserved: ratFromText(tc.reserved), incoming: ratFromText(tc.incoming), packagings: tc.packagings}
			if tc.reorderPoint != nil {
				l.reorderPoint = ratFromText(*tc.reorderPoint)
			}
			if tc.maximum != nil {
				l.maximum = ratFromText(*tc.maximum)
			}
			v := judgeLow(l)
			if v.low != tc.low {
				t.Fatalf("low = %v, want %v", v.low, tc.low)
			}
			if !tc.low {
				return
			}
			if got := decimalText(v.needed); got != tc.needed {
				t.Errorf("needed = %s, want %s", got, tc.needed)
			}
			if got := decimalText(v.suggested); got != tc.suggested {
				t.Errorf("suggested = %s, want %s", got, tc.suggested)
			}
			switch {
			case tc.packagingID == 0 && v.packaging != nil:
				t.Errorf("packaging = %+v, want none", v.packaging)
			case tc.packagingID != 0 && (v.packaging == nil || v.packaging.ID != tc.packagingID || v.count != tc.count):
				t.Errorf("packaging = %+v × %d, want %d × %d", v.packaging, v.count, tc.packagingID, tc.count)
			}
			above := ""
			if v.aboveMaximum != nil {
				above = decimalText(v.aboveMaximum)
			}
			if above != tc.aboveMaximum {
				t.Errorf("above the maximum = %q, want %q", above, tc.aboveMaximum)
			}
		})
	}
}
