package identity

import "testing"

// TestClampSearchLimit_ClampsToTheDocumentedRange pins clampSearchLimit's
// boundaries: SearchUsers never queries for fewer than one row (a caller
// asking for none, or a negative count, still gets a usable answer) or more
// than fifty (a caller asking for more never gets an unbounded query), and a
// limit already inside the range passes through unchanged.
func TestClampSearchLimit_ClampsToTheDocumentedRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "negative", limit: -1, want: 1},
		{name: "zero", limit: 0, want: 1},
		{name: "atMin", limit: 1, want: 1},
		{name: "withinRange", limit: 10, want: 10},
		{name: "atMax", limit: 50, want: 50},
		{name: "aboveMax", limit: 51, want: 50},
		{name: "wayAboveMax", limit: 1000, want: 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := clampSearchLimit(tc.limit); got != tc.want {
				t.Errorf("clampSearchLimit(%d) = %d, want %d", tc.limit, got, tc.want)
			}
		})
	}
}
