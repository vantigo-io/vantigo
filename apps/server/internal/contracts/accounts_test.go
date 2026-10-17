package contracts

import "testing"

// TestSameEmployeeNumber pins identity's comparison: trimmed, case-folded as
// upper(), and an empty number the same as no other — not even another empty
// one, so an unnumbered record never matches an unnumbered user.
func TestSameEmployeeNumber(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"E-1", "E-1", true},
		{"e-1", "E-1", true},
		{" E-1\t", "E-1", true},
		{"æ-1", "Æ-1", true},
		{"E-1", "E-2", false},
		{"E-1", "E-10", false},
		{"", "", false},
		{"  ", "", false},
		{"E-1", "", false},
		{"\u212a-1", "k-1", false}, // the Kelvin sign: equal by folding, not by upper()
	} {
		if got := SameEmployeeNumber(tc.a, tc.b); got != tc.want {
			t.Errorf("SameEmployeeNumber(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
