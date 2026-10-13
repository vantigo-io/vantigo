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

// The sickness exceptions (design S9, §3.5, plan Task 5, readings 24 and 25,
// Task 3 review L5): a sykmelding over approved ferie stands — a 100 % one
// splits the ferie, a graded one stands beside it — and over an egenmelding,
// which it trims; an egenmelding over a sykmelding is refused; over a
// requested ferie it is refused like any overlap. A graded sykmelding and a
// leave allowing a share stand together when the degree and the percent sum
// to at most 100 % (L5); two sykmeldinger never do.
func TestOverlapSickness(t *testing.T) {
	t.Parallel()
	row := func(kind, status string, allows bool, share int64) overlapRow {
		return overlapRow{First: day(t, "2026-06-08"), Last: day(t, "2026-06-12"), Kind: kind, Status: status, AllowsPercent: allows, Share: share}
	}
	sick := row(kindSickCertified, absenceRegistered, false, wholeDay)
	graded := row(kindSickCertified, absenceRegistered, false, 5000)
	for _, c := range []struct {
		name      string
		candidate overlapRow
		existing  overlapRow
		refused   bool
	}{
		{"a 100 % sykmelding over approved ferie", sick, row(kindHoliday, absenceApproved, false, wholeDay), false},
		{"a graded sykmelding over approved ferie", graded, row(kindHoliday, absenceApproved, false, wholeDay), false},
		{"a sykmelding over requested ferie", sick, row(kindHoliday, absenceRequested, false, wholeDay), true},
		{"a sykmelding over an egenmelding", sick, row(kindSickSelf, absenceRegistered, false, wholeDay), false},
		{"an egenmelding over a sykmelding", row(kindSickSelf, absenceRegistered, false, wholeDay), sick, true},
		{"ferie over a sykmelding", row(kindHoliday, absenceRequested, false, wholeDay), graded, true},
		{"a 50 % sykmelding beside a 50 % leave", graded, row("leave", absenceApproved, true, 5000), false},
		{"a 50 % leave beside a 50 % sykmelding", row("leave", absenceRequested, true, 5000), graded, false},
		{"a 50 % sykmelding beside a 60 % leave", graded, row("leave", absenceApproved, true, 6000), true},
		{"a 100 % sykmelding beside a 50 % leave", sick, row("leave", absenceApproved, true, 5000), true},
		{"two 50 % sykmeldinger", graded, graded, true},
		{"a 50 % sykmelding beside a leave without a share", graded, row("leave", absenceApproved, false, wholeDay), true},
	} {
		if got := overlapRefused(c.candidate, []overlapRow{c.existing}); got != c.refused {
			t.Errorf("%s: refused = %v, want %v", c.name, got, c.refused)
		}
	}
}
