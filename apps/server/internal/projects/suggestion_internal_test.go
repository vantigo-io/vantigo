package projects

import "testing"

// The pure letter derivation the code suggestion is built from (design §4.2,
// task 6 brief). It is worth pinning directly, not only through the
// endpoint: lettersFor and customerLetters are what the endpoint's own tests
// (suggestion_test.go) can only exercise through a customer and a counter
// value, and the tricky cases — diacritics, a one-word name, more than
// three words, a name with no letters at all — are much cheaper to name
// here.

func TestLettersFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
	}{
		{"Kraft-Verket", "KV"},
		{"Energy migration", "EM"},
		{"Website", "WE"},
		{"Ærlig Østlig Åpen", "AOA"},
		{"Émile Zola", "EZ"},
		{"A", "A"},
		{"", ""},
		{"One Two Three Four", "OTT"},
		{"123 Go", "GO"},
	}
	for _, tc := range cases {
		if got := lettersFor(tc.name); got != tc.want {
			t.Errorf("lettersFor(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCustomerLetters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		derived       string
		existingCodes []string
		want          string
	}{
		{"KV", nil, "KV"},
		{"KV", []string{"KVEM1000"}, "KV"},
		// The existing codes' leading letter runs are "KRVEM" and "KRVSO":
		// neither starts with the derived "KV", but they share the common
		// prefix "KRV", cut to the derived length (2) — "KR" — so a
		// hand-chosen customer prefix sticks.
		{"KV", []string{"KRVEM1000", "KRVSO1001"}, "KR"},
		// Neither existing code's run has anything in common with the
		// other's, so there is no shared prefix to prefer over the derived
		// letters.
		{"KV", []string{"AB1000", "CD1001"}, "KV"},
	}
	for _, tc := range cases {
		if got := customerLetters(tc.derived, tc.existingCodes); got != tc.want {
			t.Errorf("customerLetters(%q, %v) = %q, want %q", tc.derived, tc.existingCodes, got, tc.want)
		}
	}
}
