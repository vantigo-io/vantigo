package energy

import (
	"testing"
	"time"
)

// TestBillingPeriods pins design D4's billing periods (plan reading 2): the
// civil unit of the cadence in the market zone, clipped to the supply period,
// only those ended by through — whatever zone the process runs in.
func TestBillingPeriods(t *testing.T) {
	t.Parallel()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, oslo) }
	ptr := func(t time.Time) *time.Time { return &t }
	for _, tc := range []struct {
		name    string
		start   time.Time
		end     *time.Time
		cadence string
		through time.Time
		want    [][2]time.Time
	}{
		{"monthly, a whole month and one running", at(2026, 1, 1, 0), nil, "monthly", at(2026, 2, 15, 0),
			[][2]time.Time{{at(2026, 1, 1, 0), at(2026, 2, 1, 0)}}},
		{"monthly, through exactly a month's end", at(2026, 1, 1, 0), nil, "monthly", at(2026, 3, 1, 0),
			[][2]time.Time{{at(2026, 1, 1, 0), at(2026, 2, 1, 0)}, {at(2026, 2, 1, 0), at(2026, 3, 1, 0)}}},
		{"monthly, a partial first month from mid-day", at(2026, 1, 15, 14), nil, "monthly", at(2026, 3, 1, 0),
			[][2]time.Time{{at(2026, 1, 15, 14), at(2026, 2, 1, 0)}, {at(2026, 2, 1, 0), at(2026, 3, 1, 0)}}},
		{"monthly, the DST month in Oslo", at(2026, 3, 1, 0), nil, "monthly", at(2026, 4, 1, 0),
			[][2]time.Time{{at(2026, 3, 1, 0), at(2026, 4, 1, 0)}}},
		{"monthly, an ended supply period clips the last", at(2026, 1, 1, 0), ptr(at(2026, 2, 10, 0)), "monthly", at(2026, 6, 1, 0),
			[][2]time.Time{{at(2026, 1, 1, 0), at(2026, 2, 1, 0)}, {at(2026, 2, 1, 0), at(2026, 2, 10, 0)}}},
		{"monthly, an end after through is not settled", at(2026, 1, 1, 0), ptr(at(2026, 2, 10, 0)), "monthly", at(2026, 2, 5, 0),
			[][2]time.Time{{at(2026, 1, 1, 0), at(2026, 2, 1, 0)}}},
		{"quarterly from mid-February", at(2026, 2, 14, 0), nil, "quarterly", at(2026, 7, 1, 0),
			[][2]time.Time{{at(2026, 2, 14, 0), at(2026, 4, 1, 0)}, {at(2026, 4, 1, 0), at(2026, 7, 1, 0)}}},
		{"yearly", at(2025, 6, 1, 0), nil, "yearly", at(2027, 3, 1, 0),
			[][2]time.Time{{at(2025, 6, 1, 0), at(2026, 1, 1, 0)}, {at(2026, 1, 1, 0), at(2027, 1, 1, 0)}}},
		{"nothing closed yet", at(2026, 1, 1, 0), nil, "monthly", at(2026, 1, 31, 0), nil},
		{"a supply period ending where it starts settles nothing", at(2026, 1, 1, 0), ptr(at(2026, 1, 1, 0)), "monthly", at(2026, 6, 1, 0), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := billingPeriods(tc.start, tc.end, tc.cadence, oslo, tc.through)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("billingPeriods = %v, want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if !got[i].Start.Equal(w[0]) || !got[i].End.Equal(w[1]) {
					t.Errorf("period %d = [%s, %s), want [%s, %s)", i, got[i].Start, got[i].End, w[0], w[1])
				}
			}
		})
	}
	if _, err := billingPeriods(at(2026, 1, 1, 0), nil, "weekly", oslo, at(2026, 6, 1, 0)); err == nil {
		t.Error("billingPeriods accepted a cadence that is none of the three")
	}
}

// TestCountedDays pins plan reading 5: a day belongs to the period holding its
// midnight, so two periods meeting mid-day never both count it, and a DST
// day is one day.
func TestCountedDays(t *testing.T) {
	t.Parallel()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, oslo) }
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	before := countedDays(at(3, 1, 0), at(3, 5, 14), oslo)
	after := countedDays(at(3, 5, 14), at(3, 8, 0), oslo)
	if len(before) != 5 || !before[0].Equal(day(3, 1)) || !before[4].Equal(day(3, 5)) {
		t.Errorf("countedDays to the 5th at 14:00 = %v, want the 1st to the 5th", before)
	}
	if len(after) != 2 || !after[0].Equal(day(3, 6)) || !after[1].Equal(day(3, 7)) {
		t.Errorf("countedDays from the 5th at 14:00 = %v, want the 6th and the 7th", after)
	}
	if got := countedDays(at(3, 1, 0), at(4, 1, 0), oslo); len(got) != 31 {
		t.Errorf("countedDays over March (DST) = %d days, want 31", len(got))
	}
	if daysInMonth(day(2, 10)) != 28 || daysInYear(day(2, 10)) != 365 || daysInYear(time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)) != 366 {
		t.Error("daysInMonth or daysInYear is wrong")
	}
}
