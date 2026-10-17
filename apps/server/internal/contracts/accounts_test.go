package contracts

import "testing"

// TestSameEmployeeNumber pins identity's comparison, the same
// NormalizeEmployeeNumber key: trimmed, NFC-normalised, upper-cased by Go,
// and an empty number the same as no other — not
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
		{"ß-1", "ẞ-1", false},     // Go keeps ß, where an en_US upper() would make it ẞ
		{"ǅ-1", "ǆ-1", true},
	} {
		if got := SameEmployeeNumber(tc.a, tc.b); got != tc.want {
			t.Errorf("SameEmployeeNumber(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestNormalizeEmployeeNumber pins the key itself: what identity stores in
// users.employee_number_key and its unique index is on.
func TestNormalizeEmployeeNumber(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{" e-1\t", "E-1"},
		{"æ-1", "Æ-1"},
		{"a\u030a-1", "\u00c5-1"},
		{"ß-1", "ß-1"},
		{"ǆ-1", "Ǆ-1"},
		{"\u212a-1", "K-1"},
		{"  ", ""},
	} {
		if got := NormalizeEmployeeNumber(tc.in); got != tc.want {
			t.Errorf("NormalizeEmployeeNumber(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
