package staff

import "testing"

// convertVirkedager is Norwegian payroll's conversion of virkedager (Monday
// to Saturday) to working days of a five-day week (design S9, §4): ⌊v / 6⌋
// whole weeks of five, and the rest up to five — so a Monday-to-Friday week
// costs five days and an entitlement is never undercounted (31 → 26, plan
// pre-flight I3).
func TestConvertVirkedager(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ virkedager, days int }{
		{0, 0}, {5, 5}, {6, 5}, {10, 9}, {12, 10}, {18, 15}, {24, 20},
		{25, 21}, {29, 25}, {30, 25}, {31, 26}, {35, 30}, {36, 30}, {60, 50},
	} {
		if got := convertVirkedager(c.virkedager); got != c.days {
			t.Errorf("convertVirkedager(%d) = %d, want %d", c.virkedager, got, c.days)
		}
	}
}
