package invoices

import (
	"testing"
	"time"
)

// issued_late is "more than one month after the delivery ended", with the
// month's day clamped: a delivery on 1 March issued on 31 March is on time,
// though time.AddDate would put the day a month before 31 March on 3 March.
func TestIssuedLate_AMonthIsTheSameDayClamped(t *testing.T) {
	t.Parallel()
	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, c := range []struct {
		delivered, issued string
		late              bool
	}{
		{"2027-03-01", "2027-03-31", false},
		{"2027-02-28", "2027-03-28", false},
		{"2027-02-28", "2027-03-29", true},
		{"2027-01-31", "2027-02-28", false},
		{"2027-01-31", "2027-03-01", true},
		{"2028-01-31", "2028-02-29", false},
		{"2026-11-30", "2026-12-30", false},
		{"2026-11-30", "2026-12-31", true},
		{"2026-12-15", "2027-01-15", false},
		{"2026-12-15", "2027-01-16", true},
	} {
		if got := issuedLate(day(c.issued), day(c.delivered)); got != c.late {
			t.Errorf("delivered %s, issued %s: late = %v, want %v", c.delivered, c.issued, got, c.late)
		}
	}
	if issuedLate(day("2027-03-31"), time.Time{}) {
		t.Error("no delivery: late, want never")
	}
}
