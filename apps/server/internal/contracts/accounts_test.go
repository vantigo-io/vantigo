package contracts

import "testing"

// TestSameEmployeeNumber pins identity's comparison: trimmed, NFC-normalised,
// case-folded as upper(), and an empty number the same as no other — not
// even another empty one, so an unnumbered record never matches an
// unnumbered user.
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
		{"\u00c5-17", "A\u030a-17", true}, // Å precomposed and decomposed: one number once NFC
		{"\u00e5-17", "A\u030a-17", true},
		{"\u212a-1", "k-1", true}, // the Kelvin sign is K under NFC, as identity stores it
	} {
		if got := SameEmployeeNumber(tc.a, tc.b); got != tc.want {
			t.Errorf("SameEmployeeNumber(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
