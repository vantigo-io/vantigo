package staff

import (
	"testing"
	"time"
)

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

// statutory is the seeded scheme: 25 virkedager, 6 more at 60, 6 after
// September, 12 agreed carry-over.
var statutory = accountScheme{Entitlement: 25, ExtraFrom60: 6, AfterSeptember: 6, MaxCarryOver: 12}

// days is n working days in the account's unit.
func days(n float64) int64 { return int64(n * dayUnit) }

// holidayRow is an approved, requested or registered row of a type drawing
// on the account, whole days unless share says otherwise.
func holidayRow(t *testing.T, first, last, status string, share int64) accountAbsence {
	t.Helper()
	return accountAbsence{First: day(t, first), Last: day(t, last), Status: status, Share: share}
}

// The holiday account (design §4, S9, plan Task 3): the entitlement of the
// earliest employment's scheme in the year, converted; after-September; the
// year one turns sixty; agreed carry-in capped by the scheme and forced and
// corrections not; taken and pending on working days of that year only,
// times the share; and the derived carry-in, only from the year automatic
// carry-over was turned on.
func TestHolidayAccount(t *testing.T) {
	t.Parallel()
	since2019 := []accountEmployment{{Start: day(t, "2019-03-01"), Scheme: statutory}}
	born := func(s string) *time.Time { d := day(t, s); return &d }
	from := func(y int) *int { return &y }
	for _, c := range []struct {
		name string
		in   accountInput
		year int
		want holidayAccount
	}{
		{"a Monday-to-Friday week costs five of 21",
			accountInput{Employments: since2019, Absences: []accountAbsence{holidayRow(t, "2026-06-08", "2026-06-12", "approved", 10000)}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Taken: days(5), Remaining: days(16)}},
		{"public holidays and weekends are not deducted",
			accountInput{Employments: since2019, Absences: []accountAbsence{holidayRow(t, "2026-03-30", "2026-04-06", "approved", 10000)}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Taken: days(3), Remaining: days(18)}},
		{"a registered row counts as taken, half a day each at 50 %",
			accountInput{Employments: since2019, Absences: []accountAbsence{holidayRow(t, "2026-06-08", "2026-06-12", "registered", 5000)}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Taken: days(2.5), Remaining: days(18.5)}},
		{"rejected and cancelled rows count nothing",
			accountInput{Employments: since2019, Absences: []accountAbsence{
				holidayRow(t, "2026-06-08", "2026-06-12", "rejected", 10000), holidayRow(t, "2026-06-15", "2026-06-19", "cancelled", 10000)}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Remaining: days(21)}},
		{"pending is shown apart",
			accountInput{Employments: since2019, Absences: []accountAbsence{holidayRow(t, "2026-07-06", "2026-07-08", "requested", 10000)}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Pending: days(3), Remaining: days(21)}},
		{"requesting beyond what remains is overdrawn, a warning",
			accountInput{Employments: since2019, Absences: []accountAbsence{
				holidayRow(t, "2026-06-01", "2026-06-26", "approved", 10000), holidayRow(t, "2026-07-06", "2026-07-08", "requested", 10000)}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Taken: days(20), Pending: days(3), Remaining: days(1), Overdrawn: true}},
		{"a start after 30 September earns the after-September days",
			accountInput{Employments: []accountEmployment{{Start: day(t, "2026-10-01"), Scheme: statutory}}},
			2026, holidayAccount{Year: 2026, Entitlement: days(5), Remaining: days(5)}},
		{"a start on 30 September earns the year's",
			accountInput{Employments: []accountEmployment{{Start: day(t, "2026-09-30"), Scheme: statutory}}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Remaining: days(21)}},
		{"the year one turns sixty adds the extra days, un-prorated",
			accountInput{Employments: since2019, BirthDate: born("1966-12-31")},
			2026, holidayAccount{Year: 2026, Entitlement: days(26), Remaining: days(26)}},
		// Ferieloven § 5 nr. 2 sets no start condition for the extra week,
		// and nr. 3 limits only the feriefritid (checked on Lovdata
		// 2026-10-14): a start after 30 September in the sixtieth year earns
		// both.
		{"a start after 30 September in the year one turns sixty keeps the extra days",
			accountInput{Employments: []accountEmployment{{Start: day(t, "2026-10-01"), Scheme: statutory}}, BirthDate: born("1966-12-31")},
			2026, holidayAccount{Year: 2026, Entitlement: days(10), Remaining: days(10)}},
		{"fifty-nine is not sixty",
			accountInput{Employments: since2019, BirthDate: born("1967-01-01")},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), Remaining: days(21)}},
		{"agreed carry-in is capped by the scheme, forced and corrections are not",
			accountInput{Employments: since2019, Adjustments: []accountAdjustment{
				{Year: 2026, Tenths: 150, Reason: "agreed_carry_over", FromYear: from(2025)},
				{Year: 2026, Tenths: 30, Reason: "forced_carry_over", FromYear: from(2024)},
				{Year: 2026, Tenths: -15, Reason: "correction"},
				{Year: 2025, Tenths: 50, Reason: "correction"}}},
			2026, holidayAccount{Year: 2026, Entitlement: days(21), CarriedIn: days(10 + 3 - 1.5), Remaining: days(21 + 10 + 3 - 1.5)}},
		{"no employment in the year earns nothing",
			accountInput{Employments: []accountEmployment{{Start: day(t, "2019-01-01"), End: born("2025-12-31"), Scheme: statutory}}},
			2026, holidayAccount{Year: 2026}},
		{"two concurrent employments read the earlier's scheme",
			accountInput{Employments: []accountEmployment{
				{Start: day(t, "2024-01-01"), Scheme: statutory},
				{Start: day(t, "2019-01-01"), Scheme: accountScheme{Entitlement: 30, ExtraFrom60: 6, AfterSeptember: 6, MaxCarryOver: 12}}}},
			2026, holidayAccount{Year: 2026, Entitlement: days(25), Remaining: days(25)}},
	} {
		if got := accountFor(c.in, c.year); got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// A row across New Year is two accounts' worth (design §9): each year counts
// its own working days of it — four in 2026 (28 to 31 December), two in 2027
// (1 January a holiday, then the 4th and the 5th).
func TestHolidayAccount_ARowOverNewYear(t *testing.T) {
	t.Parallel()
	in := accountInput{
		Employments: []accountEmployment{{Start: day(t, "2019-01-01"), Scheme: statutory}},
		Absences:    []accountAbsence{holidayRow(t, "2026-12-28", "2027-01-05", "approved", 10000)},
	}
	if got := accountFor(in, 2026).Taken; got != days(4) {
		t.Errorf("2026 taken = %d, want %d", got, days(4))
	}
	if got := accountFor(in, 2027).Taken; got != days(2) {
		t.Errorf("2027 taken = %d, want %d", got, days(2))
	}
}

// Automatic carry-over (plan pre-flight M6, reading 5): with it on, a year
// whose 1 January is on or after the day it was turned on, with no
// carry-over recorded from the year before, derives a forced carry-in of that
// year's positive remaining — so turning it on never reaches back into a year
// already under way, whose carry-in HR settled; never when it is off, never
// beside a carry-over recorded by hand, and never a negative one.
func TestHolidayAccount_DerivedCarryIn(t *testing.T) {
	t.Parallel()
	since := func(s string) *time.Time { d := day(t, s); return &d }
	from := func(y int) *int { return &y }
	base := accountInput{
		Employments: []accountEmployment{{Start: day(t, "2019-01-01"), Scheme: statutory}},
		// 16 of 2025's 21 days taken: 5 remain.
		Absences: []accountAbsence{
			holidayRow(t, "2025-06-16", "2025-07-04", "approved", 10000),
			holidayRow(t, "2025-07-07", "2025-07-07", "approved", 10000),
		},
	}
	if got := accountFor(base, 2025).Remaining; got != days(5) {
		t.Fatalf("2025 remaining = %d, want %d", got, days(5))
	}
	for _, c := range []struct {
		name  string
		since *time.Time
		adj   []accountAdjustment
		want  int64
	}{
		{"off", nil, nil, 0},
		{"turned on before the year began", since("2025-11-20"), nil, days(5)},
		{"turned on on 1 January", since("2026-01-01"), nil, days(5)},
		{"turned on during the year", since("2026-03-01"), nil, 0},
		{"turned on the year after", since("2027-01-10"), nil, 0},
		{"a carry-over recorded by hand", since("2026-01-01"),
			[]accountAdjustment{{Year: 2026, Tenths: 20, Reason: "agreed_carry_over", FromYear: from(2025)}}, 0},
	} {
		in := base
		in.AutoCarryOverSince = c.since
		in.Adjustments = c.adj
		got := accountFor(in, 2026)
		if got.CarriedInDerived != c.want {
			t.Errorf("%s: carriedInDerived = %d, want %d", c.name, got.CarriedInDerived, c.want)
		}
		if got.Remaining != days(21)+got.CarriedIn+c.want {
			t.Errorf("%s: remaining = %d, want the entitlement and both carry-ins", c.name, got.Remaining)
		}
	}
	overdrawn := base
	overdrawn.Absences = append(overdrawn.Absences, holidayRow(t, "2025-08-04", "2025-08-15", "approved", 10000))
	overdrawn.AutoCarryOverSince = since("2026-01-01")
	if got := accountFor(overdrawn, 2026).CarriedInDerived; got != 0 {
		t.Errorf("a negative remaining carried in %d, want nothing", got)
	}
}
