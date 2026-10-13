package staff

import "testing"

// Overlap (design §3.5, plan Task 3, readings 17 and 27): on any day two day
// rows of one person stand together only when both types allow a share and
// the shares of every row that day sum to at most 100 %; hour rows overlap
// only hour rows, by the hours of a day summed to at most 24.
func TestOverlap(t *testing.T) {
	t.Parallel()
	dayRow := func(first, last string, allows bool, share int64) overlapRow {
		return overlapRow{First: day(t, first), Last: day(t, last), AllowsPercent: allows, Share: share}
	}
	hourRow := func(on string, hundredths int64) overlapRow {
		return overlapRow{First: day(t, on), Last: day(t, on), Hours: hundredths}
	}
	week := dayRow("2026-06-08", "2026-06-12", false, wholeDay)
	half := dayRow("2026-06-08", "2026-06-12", true, 5000)
	for _, c := range []struct {
		name      string
		candidate overlapRow
		existing  []overlapRow
		refused   bool
	}{
		{"nothing there", week, nil, false},
		{"the next week", dayRow("2026-06-15", "2026-06-19", false, wholeDay), []overlapRow{week}, false},
		{"one shared day of a whole-day type", dayRow("2026-06-12", "2026-06-15", false, wholeDay), []overlapRow{week}, true},
		{"two 50 % rows", half, []overlapRow{half}, false},
		{"50 % and 60 %", dayRow("2026-06-10", "2026-06-10", true, 6000), []overlapRow{half}, true},
		{"a 50 % row beside a type without a share", half, []overlapRow{dayRow("2026-06-10", "2026-06-10", false, 5000)}, true},
		{"the sum is per day: a third row on a day two already fill",
			dayRow("2026-06-10", "2026-06-10", true, 1000), []overlapRow{half, dayRow("2026-06-10", "2026-06-10", true, 5000)}, true},
		{"the sum is per day: a third row on a day with room",
			dayRow("2026-06-08", "2026-06-08", true, 1000), []overlapRow{half, dayRow("2026-06-10", "2026-06-10", true, 5000)}, false},
		{"an hour row inside a whole day row", hourRow("2026-06-10", 200), []overlapRow{week}, false},
		{"a day row over an hour row", week, []overlapRow{hourRow("2026-06-10", 200)}, false},
		{"two hour rows within 24 hours", hourRow("2026-06-10", 400), []overlapRow{hourRow("2026-06-10", 400)}, false},
		{"hour rows past 24 hours", hourRow("2026-06-10", 500), []overlapRow{hourRow("2026-06-10", 2000)}, true},
		{"hour rows on other days", hourRow("2026-06-10", 2400), []overlapRow{hourRow("2026-06-11", 2400)}, false},
	} {
		if got := overlapRefused(c.candidate, c.existing); got != c.refused {
			t.Errorf("%s: refused = %v, want %v", c.name, got, c.refused)
		}
	}
}
