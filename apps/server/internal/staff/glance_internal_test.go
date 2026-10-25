package staff

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// Tenure is whole months from the run's start: a month is reached on the
// same day of the month, or on the last day of a month without that day.
func TestWholeMonths(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		from, to string
		want     int
	}{
		{"2026-04-15", "2026-04-15", 0},
		{"2026-04-15", "2026-05-14", 0},
		{"2026-04-15", "2026-05-15", 1},
		{"2026-04-15", "2026-09-12", 4},
		{"2024-01-31", "2024-02-29", 1},
		{"2024-01-31", "2024-02-28", 0},
		{"2019-01-01", "2026-09-12", 92},
		{"2026-09-13", "2026-09-12", 0},
	} {
		if got := wholeMonths(day(t, c.from), day(t, c.to)); got != c.want {
			t.Errorf("wholeMonths(%s, %s) = %d, want %d", c.from, c.to, got, c.want)
		}
	}
}

// The expected week is the full-time hours times the percentage, rounded to
// the nearest quarter of an hour; no terms in force, no expected week.
func TestExpectedHours(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		full, fte int64 // hundredths
		want      float64
	}{
		{3750, 8000, 30},
		{3750, 10000, 37.5},
		{3750, 3333, 12.5}, // 12.49875
		{3750, 5500, 20.75},
		{4000, 6670, 26.75}, // 26.68
		{3750, 1000, 3.75},
	} {
		got := expectedHours([]*store.StaffEmploymentTerm{{FullTimeHoursPerWeek: numericOfHundredths(c.full), FtePercent: numericOfHundredths(c.fte)}})
		if got == nil || *got != c.want {
			t.Errorf("expectedHours(%d, %d) = %v, want %v", c.full, c.fte, got, c.want)
		}
	}
	if got := expectedHours(nil); got != nil {
		t.Errorf("expectedHours(nil) = %v, want nil", *got)
	}
	for in, want := range map[float64]float64{0.12: 0, 0.13: 0.25, 7.37: 7.25, 7.38: 7.5, 7.875: 8} {
		if got := quarterHours(in); got != want {
			t.Errorf("quarterHours(%v) = %v, want %v", in, got, want)
		}
	}
}

// The expected week is every employment's in force today, summed and then
// rounded: two concurrent employments at 50 % of 37.5 hours expect the full
// 37.5, a single one at 80 % expects 30, and an employment ended or not yet
// begun, or one without terms in force, adds nothing. The title and the
// percentage stay the place's (placeTermsOf).
func TestExpectedWeekOf(t *testing.T) {
	t.Parallel()
	today := day(t, "2026-09-12")
	employment := func(id int64, start, end string) store.StaffEmployment {
		out := store.StaffEmployment{ID: id, StartDate: dayParam(day(t, start))}
		if end != "" {
			out.EndDate = dayParam(day(t, end))
		}
		return out
	}
	terms := func(id, fte int64, from string) store.StaffEmploymentTerm {
		return store.StaffEmploymentTerm{
			EmploymentID: id, ValidFrom: dayParam(day(t, from)),
			FullTimeHoursPerWeek: numericOfHundredths(3750), FtePercent: numericOfHundredths(fte * 100),
		}
	}
	for _, c := range []struct {
		name        string
		employments []store.StaffEmployment
		chains      map[int64][]store.StaffEmploymentTerm
		want        float64 // -1 for none
		placeFTE    float64
	}{
		{
			"two at 50 %",
			[]store.StaffEmployment{employment(1, "2024-01-01", ""), employment(2, "2025-01-01", "")},
			map[int64][]store.StaffEmploymentTerm{1: {terms(1, 50, "2024-01-01")}, 2: {terms(2, 50, "2025-01-01")}},
			37.5, 50,
		},
		{
			"one at 80 %",
			[]store.StaffEmployment{employment(1, "2024-01-01", "")},
			map[int64][]store.StaffEmploymentTerm{1: {terms(1, 80, "2024-01-01")}},
			30, 80,
		},
		{
			"one ended, one ahead, one without terms",
			[]store.StaffEmployment{employment(1, "2024-01-01", ""), employment(2, "2020-01-01", "2026-09-11"), employment(3, "2026-10-01", ""), employment(4, "2025-01-01", "")},
			map[int64][]store.StaffEmploymentTerm{1: {terms(1, 60, "2024-01-01")}, 2: {terms(2, 40, "2020-01-01")}, 3: {terms(3, 40, "2026-10-01")}},
			22.5, 60,
		},
		{"none in force", []store.StaffEmployment{employment(1, "2020-01-01", "2026-09-11")}, map[int64][]store.StaffEmploymentTerm{1: {terms(1, 100, "2020-01-01")}}, -1, -1},
	} {
		got := expectedWeekOf(c.employments, c.chains, today)
		if (got == nil) != (c.want < 0) || (got != nil && *got != c.want) {
			t.Errorf("%s: expectedWeekOf = %v, want %v", c.name, got, c.want)
		}
		place := placeTermsOf(c.employments, c.chains, today)
		if (place == nil) != (c.placeFTE < 0) || (place != nil && numberOf(place.FtePercent) != c.placeFTE) {
			t.Errorf("%s: the place's terms = %+v, want %v %%", c.name, place, c.placeFTE)
		}
	}
}

// The week starts on the Monday on or before today, the business zone's
// calendar day: an instant late on Sunday in UTC that is already Monday in
// Oslo is that Monday's week, and New Year's Day on a Friday is the week of
// the Monday before it, in the year before.
func TestWeekStartOf(t *testing.T) {
	t.Parallel()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		now  time.Time
		zone *time.Location
		want string
	}{
		{"a Saturday", time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC), oslo, "2026-09-07"},
		{"a Monday", time.Date(2026, time.September, 7, 9, 0, 0, 0, time.UTC), oslo, "2026-09-07"},
		{"a Sunday", time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC), oslo, "2026-09-07"},
		{"Sunday night in UTC, Monday in Oslo", time.Date(2026, time.September, 13, 22, 30, 0, 0, time.UTC), oslo, "2026-09-14"},
		{"Sunday night in UTC, read in UTC", time.Date(2026, time.September, 13, 22, 30, 0, 0, time.UTC), time.UTC, "2026-09-07"},
		{"New Year's Day 2027, a Friday", time.Date(2026, time.December, 31, 23, 30, 0, 0, time.UTC), oslo, "2026-12-28"},
		{"4 January 2027, a Monday", time.Date(2027, time.January, 4, 8, 0, 0, 0, time.UTC), oslo, "2027-01-04"},
	} {
		got := weekStartOf(civilToday(c.now, c.zone))
		if got.Format(time.DateOnly) != c.want || got.Weekday() != time.Monday || got.Location() != time.UTC || got.Hour() != 0 {
			t.Errorf("%s: weekStartOf = %s, want %s at midnight UTC", c.name, got, c.want)
		}
	}
}

// The holiday is split against today: taken is the approved and registered
// working days up to today, this year's only; planned the approved,
// registered and requested days after it, to the year's end — a row across
// today counts on both sides, each day times its share.
func TestHolidaySplit(t *testing.T) {
	t.Parallel()
	row := func(first, last, status string, share int64) accountAbsence {
		return accountAbsence{First: day(t, first), Last: day(t, last), Status: status, Share: share}
	}
	taken, planned := holidaySplit([]accountAbsence{
		row("2025-12-29", "2026-01-02", absenceApproved, wholeDay), // 1 day in 2026: 1 January is a public holiday
		row("2026-08-10", "2026-08-14", absenceApproved, wholeDay), // 5 taken
		row("2026-09-10", "2026-09-15", absenceApproved, wholeDay), // 2 taken (10, 11), 2 planned (14, 15)
		row("2026-10-01", "2026-10-02", absenceRequested, wholeDay),
		row("2026-10-06", "2026-10-08", absenceApproved, wholeDay),
		row("2026-11-02", "2026-11-06", absenceApproved, wholeDay/2), // 2.5
		row("2026-12-30", "2027-01-05", absenceRegistered, wholeDay), // 2 planned this year
		row("2026-06-01", "2026-06-05", absenceRejected, wholeDay),
		row("2026-06-08", "2026-06-12", absenceCancelled, wholeDay),
		row("2026-07-01", "2026-07-03", absenceRequested, wholeDay), // a request in the past is neither
	}, day(t, "2026-09-12"))
	if daysOf(taken) != 8 || daysOf(planned) != 11.5 {
		t.Errorf("holidaySplit = %v taken, %v planned, want 8 and 11.5", daysOf(taken), daysOf(planned))
	}
}

// The year's sick days are counted as the payroll file counts them: a
// sykmelding by its degree, an egenmelding whole, never a weekend, and a
// holiday day counts no sickness under it; last year's days are not this
// year's.
func TestSickDaysOf(t *testing.T) {
	t.Parallel()
	degree := func(p int64) pgtype.Numeric { return numericOfHundredths(p * 100) }
	row := func(kind, first, last string, deg pgtype.Numeric) store.AbsencesBetweenRow {
		return store.AbsencesBetweenRow{Kind: kind, Unit: unitDays, FirstDay: dayParam(day(t, first)), LastDay: dayParam(day(t, last)), DegreePercent: deg, Status: absenceApproved}
	}
	self, certified := sickDaysOf([]store.AbsencesBetweenRow{
		row(kindSickSelf, "2026-07-07", "2026-07-09", pgtype.Numeric{}),      // 3
		row(kindSickCertified, "2026-09-09", "2026-09-14", degree(50)),       // 4 working days at 50 %
		row(kindSickCertified, "2026-11-02", "2026-11-06", pgtype.Numeric{}), // 5, but 2 under ferie
		row(kindHoliday, "2026-11-05", "2026-11-06", pgtype.Numeric{}),       // the holiday days
		row(kindSickSelf, "2025-12-30", "2026-01-02", pgtype.Numeric{}),      // 1 in 2026 (2 Jan)
		row(kindChildSick, "2026-03-03", "2026-03-03", pgtype.Numeric{}),     // no sickness
		row(kindSickCertified, "2027-01-04", "2027-01-08", pgtype.Numeric{}), // next year
	}, 2026)
	if float64(self)/wholeDay != 4 || float64(certified)/wholeDay != 5 {
		t.Errorf("sickDaysOf = %v self, %v certified, want 4 and 5", float64(self)/wholeDay, float64(certified)/wholeDay)
	}
}

// The probation's end is the extension's when one is agreed, and none once
// it has passed; the last day itself still shows.
func TestProbationEnd(t *testing.T) {
	t.Parallel()
	today := day(t, "2026-09-12")
	for _, c := range []struct {
		name string
		t    store.StaffEmploymentTerm
		want string
	}{
		{"none", store.StaffEmploymentTerm{}, ""},
		{"ahead", store.StaffEmploymentTerm{ProbationEndDate: dayParam(day(t, "2026-10-14"))}, "2026-10-14"},
		{"today", store.StaffEmploymentTerm{ProbationEndDate: dayParam(today)}, "2026-09-12"},
		{"ended", store.StaffEmploymentTerm{ProbationEndDate: dayParam(day(t, "2026-09-11"))}, ""},
		{"extended", store.StaffEmploymentTerm{ProbationEndDate: dayParam(day(t, "2026-09-01")), ProbationExtensionAgreed: true, ProbationExtendedTo: dayParam(day(t, "2026-11-01"))}, "2026-11-01"},
	} {
		got := probationEnd(&c.t, today)
		if (got == nil) != (c.want == "") || (got != nil && got.Format(time.DateOnly) != c.want) {
			t.Errorf("%s: probationEnd = %v, want %q", c.name, got, c.want)
		}
	}
}

// The place is the earliest employment in force today by start, then id; an
// employment ended or not yet begun is none.
func TestPlaceOf(t *testing.T) {
	t.Parallel()
	today := day(t, "2026-09-12")
	e := func(id int64, start, end string) store.StaffEmployment {
		out := store.StaffEmployment{ID: id, StartDate: dayParam(day(t, start))}
		if end != "" {
			out.EndDate = dayParam(day(t, end))
		}
		return out
	}
	if p := placeOf([]store.StaffEmployment{e(1, "2020-01-01", "2026-09-11"), e(2, "2026-10-01", ""), e(4, "2024-01-01", ""), e(3, "2024-01-01", "")}, today); p == nil || p.ID != 3 {
		t.Errorf("placeOf = %+v, want employment 3", p)
	}
	if p := placeOf([]store.StaffEmployment{e(1, "2020-01-01", "2026-09-11")}, today); p != nil {
		t.Errorf("placeOf without an employment in force = %+v, want nil", p)
	}
}
