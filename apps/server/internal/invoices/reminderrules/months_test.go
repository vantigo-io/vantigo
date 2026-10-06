package reminderrules

import (
	"testing"
	"time"
)

// D8's month helper: the same day n months on, clamped to that month's last
// day — never AddDate's overflow into the next month.
func TestReminderRules_AddMonthsClamped(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		from string
		n    int
		want string
	}{
		{"2026-08-31", 6, "2027-02-28"}, // non-leap February; AddDate gives 2027-03-03
		{"2027-08-31", 6, "2028-02-29"}, // leap February
		{"2026-03-31", 6, "2026-09-30"},
		{"2026-09-15", 6, "2027-03-15"},
		{"2027-03-15", -6, "2026-09-15"}, // back across a year
		{"2027-02-28", -6, "2026-08-28"},
		{"2026-07-01", 6, "2027-01-01"},
		{"2026-01-31", 1, "2026-02-28"},
		{"2026-12-31", 2, "2027-02-28"},
		{"2026-01-15", -13, "2024-12-15"},
	} {
		got := AddMonthsClamped(day(c.from), c.n)
		if !got.Equal(day(c.want)) {
			t.Errorf("AddMonthsClamped(%s, %d) = %s, want %s", c.from, c.n, got.Format(time.DateOnly), c.want)
		}
	}
	if got := day("2026-08-31").AddDate(0, 6, 0); got.Equal(AddMonthsClamped(day("2026-08-31"), 6)) {
		t.Errorf("the helper agrees with AddDate's %s; it must clamp", got.Format(time.DateOnly))
	}
}

// R4 §2.7's lenient reading: a weekend due date moves to the Monday after;
// holidays are not moved (reading 8).
func TestReminderRules_EffectiveDue(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ due, want string }{
		{"2026-10-10", "2026-10-12"}, // Saturday → Monday
		{"2026-10-11", "2026-10-12"}, // Sunday → Monday
		{"2026-10-12", "2026-10-12"}, // Monday
		{"2026-10-09", "2026-10-09"}, // Friday
		{"2026-12-25", "2026-12-25"}, // Christmas Day, a Friday: a holiday is not moved
	} {
		if got := EffectiveDue(day(c.due)); !got.Equal(day(c.want)) {
			t.Errorf("EffectiveDue(%s) = %s, want %s", c.due, got.Format(time.DateOnly), c.want)
		}
	}
}
