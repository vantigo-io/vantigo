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
// Task 5 review H1, M1, M2): a sykmelding stands over ferie — approved or
// requested, which it leaves as it is (a request is then refused at its
// approval) — and over an egenmelding, which it trims; an egenmelding over a
// sykmelding is refused. A sykmelding of any degree stands beside leave that
// allows a share while that leave leaves part of the day (under 100 %); two
// sykmeldinger never share a day.
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
		existing  []overlapRow
		refused   bool
	}{
		{"a 100 % sykmelding over approved ferie", sick, []overlapRow{row(kindHoliday, absenceApproved, false, wholeDay)}, false},
		{"a graded sykmelding over approved ferie", graded, []overlapRow{row(kindHoliday, absenceApproved, false, wholeDay)}, false},
		{"a sykmelding over requested ferie", sick, []overlapRow{row(kindHoliday, absenceRequested, false, wholeDay)}, false},
		{"a sykmelding over an egenmelding", sick, []overlapRow{row(kindSickSelf, absenceRegistered, false, wholeDay)}, false},
		{"an egenmelding over a sykmelding", row(kindSickSelf, absenceRegistered, false, wholeDay), []overlapRow{sick}, true},
		{"ferie over a sykmelding", row(kindHoliday, absenceRequested, false, wholeDay), []overlapRow{graded}, true},
		{"a 50 % sykmelding beside a 50 % leave", graded, []overlapRow{row("leave", absenceApproved, true, 5000)}, false},
		{"a 50 % leave beside a 50 % sykmelding", row("leave", absenceRequested, true, 5000), []overlapRow{graded}, false},
		{"a 50 % sykmelding beside a 60 % leave", graded, []overlapRow{row("leave", absenceApproved, true, 6000)}, false},
		{"a 100 % sykmelding beside a 50 % leave", sick, []overlapRow{row("leave", absenceApproved, true, 5000)}, false},
		{"a sykmelding beside a 100 % leave", graded, []overlapRow{row("leave", absenceApproved, true, wholeDay)}, true},
		{"a sykmelding beside two leaves filling the day", graded, []overlapRow{row("leave", absenceApproved, true, 6000), row("leave", absenceApproved, true, 4000)}, true},
		{"a 100 % leave beside a sykmelding", row("leave", absenceRequested, true, wholeDay), []overlapRow{graded}, true},
		{"a 40 % leave beside a sykmelding and a 60 % leave", row("leave", absenceRequested, true, 4000), []overlapRow{graded, row("leave", absenceApproved, true, 6000)}, true},
		{"two 50 % sykmeldinger", graded, []overlapRow{graded}, true},
		{"a 50 % sykmelding beside a leave without a share", graded, []overlapRow{row("leave", absenceApproved, false, wholeDay)}, true},
	} {
		if got := overlapRefused(c.candidate, c.existing); got != c.refused {
			t.Errorf("%s: refused = %v, want %v", c.name, got, c.refused)
		}
	}
}
