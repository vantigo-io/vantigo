package knowledge

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestKeyBetween_OrdersAndNeverEndsInZero(t *testing.T) {
	first, err := keyBetween("", "")
	if err != nil || first == "" {
		t.Fatalf("keyBetween(\"\", \"\") = %q, %v", first, err)
	}
	for _, tc := range []struct{ a, b string }{
		{"", "V"}, {"V", ""}, {"V", "W"}, {"V", "V1"}, {"a", "a01"}, {"", "01"}, {"zz", ""}, {"1", "2"}, {"Vz", "W"},
	} {
		k, err := keyBetween(tc.a, tc.b)
		if err != nil {
			t.Fatalf("keyBetween(%q, %q): %v", tc.a, tc.b, err)
		}
		if (tc.a != "" && k <= tc.a) || (tc.b != "" && k >= tc.b) || !validKey(k) {
			t.Errorf("keyBetween(%q, %q) = %q, not strictly between or not a key", tc.a, tc.b, k)
		}
	}
	for _, tc := range []struct{ a, b string }{{"W", "V"}, {"V", "V"}, {"V0", ""}, {"", "x-"}} {
		if _, err := keyBetween(tc.a, tc.b); err == nil {
			t.Errorf("keyBetween(%q, %q) accepted bounds that are out of order or not keys", tc.a, tc.b)
		}
	}
}

// Random inserts between random neighbours keep a strictly ordered list, the
// way siblings are ordered by the C collation.
func TestKeyBetween_RandomInsertsStayOrdered(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	keys := []string{}
	for range 2000 {
		i := r.IntN(len(keys) + 1)
		a, b := "", ""
		if i > 0 {
			a = keys[i-1]
		}
		if i < len(keys) {
			b = keys[i]
		}
		// A run of inserts at the front and at the end too.
		if r.IntN(10) == 0 {
			i, a = 0, ""
			if len(keys) > 0 {
				b = keys[0]
			}
		}
		k, err := keyBetween(a, b)
		if err != nil {
			t.Fatalf("keyBetween(%q, %q): %v", a, b, err)
		}
		keys = slices.Insert(keys, i, k)
	}
	if !slices.IsSorted(keys) {
		t.Fatal("the keys are not in order")
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] == keys[i] {
			t.Fatalf("duplicate key %q", keys[i])
		}
	}
}
