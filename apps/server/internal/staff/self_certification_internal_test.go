package staff

import "testing"

// selfDays is an egenmelding row's days.
func selfDays(t *testing.T, first, last string) daySpan {
	t.Helper()
	return daySpan{First: day(t, first), Last: day(t, last)}
}

// The limits key on the policy's name (plan Task 5, Task 2 review): under
// minimum and extended the preset's numbers, whatever the row holds; under
// custom the stored ones, both judged.
func TestSelfCertLimits(t *testing.T) {
	t.Parallel()
	stored := selfCertPolicy{DaysPerOccurrence: 5, OccurrencesPerYear: 6, DaysPerYear: 20}
	for _, c := range []struct {
		policy string
		want   selfCertLimits
	}{
		{selfCertMinimum, selfCertLimits{DaysPerOccurrence: 3, OccurrencesPerYear: 4, JudgeOccurrences: true}},
		{selfCertExtended, selfCertLimits{DaysPerOccurrence: 8, DaysPerYear: 24, JudgeDays: true}},
		{selfCertCustom, selfCertLimits{DaysPerOccurrence: 5, OccurrencesPerYear: 6, DaysPerYear: 20, JudgeOccurrences: true, JudgeDays: true}},
	} {
		p := stored
		p.Policy = c.policy
		if got := limitsOf(p); got != c.want {
			t.Errorf("%s: limits = %+v, want %+v", c.policy, got, c.want)
		}
	}
}

// An occurrence is a maximal run of contiguous egenmelding days — adjacent
// rows, or rows with only Saturdays, Sundays and public holidays between —
// counted in calendar days, the bridged ones included (plan pre-flight I2,
// Revision 2 Q7a, R08-00 § 8-24). The per-occurrence cap is judged on the
// run the new row joins; the minimum counts earlier occurrences in a rolling
// year, the extended policy the days.
func TestJudgeSelfCert(t *testing.T) {
	t.Parallel()
	minimum := limitsOf(selfCertPolicy{Policy: selfCertMinimum})
	extended := limitsOf(selfCertPolicy{Policy: selfCertExtended})
	for _, c := range []struct {
		name      string
		limits    selfCertLimits
		existing  []daySpan
		candidate daySpan
		want      string
	}{
		{"three days", minimum, nil, selfDays(t, "2026-10-05", "2026-10-07"), ""},
		{"four days at once", minimum, nil, selfDays(t, "2026-10-05", "2026-10-08"), selfCertOverOccurrence},
		{"1+1+1+1 rows on four consecutive weekdays: the fourth is refused", minimum,
			[]daySpan{selfDays(t, "2026-10-05", "2026-10-05"), selfDays(t, "2026-10-06", "2026-10-06"), selfDays(t, "2026-10-07", "2026-10-07")},
			selfDays(t, "2026-10-08", "2026-10-08"), selfCertOverOccurrence},
		{"two contiguous rows are one occurrence", minimum,
			[]daySpan{selfDays(t, "2026-10-05", "2026-10-05")}, selfDays(t, "2026-10-06", "2026-10-07"), ""},
		{"Friday and again Monday: the weekend counts, Monday is day four", minimum,
			[]daySpan{selfDays(t, "2026-10-09", "2026-10-09")}, selfDays(t, "2026-10-12", "2026-10-12"), selfCertOverOccurrence},
		{"two short occurrences ten days apart", minimum,
			[]daySpan{selfDays(t, "2026-10-05", "2026-10-06")}, selfDays(t, "2026-10-16", "2026-10-16"), ""},
		{"the fifth occurrence in a rolling year", minimum,
			[]daySpan{selfDays(t, "2026-01-05", "2026-01-05"), selfDays(t, "2026-03-02", "2026-03-02"), selfDays(t, "2026-05-04", "2026-05-04"), selfDays(t, "2026-07-06", "2026-07-06")},
			selfDays(t, "2026-10-05", "2026-10-05"), selfCertOverOccurrences},
		{"the fifth, a year and more after the first", minimum,
			[]daySpan{selfDays(t, "2025-09-01", "2025-09-01"), selfDays(t, "2026-03-02", "2026-03-02"), selfDays(t, "2026-05-04", "2026-05-04"), selfDays(t, "2026-07-06", "2026-07-06")},
			selfDays(t, "2026-10-05", "2026-10-05"), ""},
		{"eight days under extended", extended, nil, selfDays(t, "2026-10-05", "2026-10-12"), ""},
		{"nine days under extended", extended, nil, selfDays(t, "2026-10-05", "2026-10-13"), selfCertOverOccurrence},
		{"the 24th day under extended", extended,
			[]daySpan{selfDays(t, "2026-02-02", "2026-02-09"), selfDays(t, "2026-04-06", "2026-04-13")}, selfDays(t, "2026-09-07", "2026-09-14"), ""},
		{"the 25th day under extended", extended,
			[]daySpan{selfDays(t, "2026-02-02", "2026-02-09"), selfDays(t, "2026-04-06", "2026-04-13"), selfDays(t, "2026-09-07", "2026-09-14")},
			selfDays(t, "2026-10-05", "2026-10-05"), selfCertOverDays},
	} {
		if got := judgeSelfCert(c.limits, c.existing, c.candidate); got != c.want {
			t.Errorf("%s: refusal = %q, want %q", c.name, got, c.want)
		}
	}
}

// The standing today (GET .../self-certification): the occurrences and the
// days in the rolling year ending today.
func TestSelfCertStanding(t *testing.T) {
	t.Parallel()
	rows := []daySpan{selfDays(t, "2025-09-01", "2025-09-02"), selfDays(t, "2026-03-02", "2026-03-03"), selfDays(t, "2026-10-09", "2026-10-09"), selfDays(t, "2026-10-12", "2026-10-12")}
	occurrences, total := selfCertUsed(rows, day(t, "2026-10-20"))
	if occurrences != 2 || total != 6 {
		t.Errorf("used = %d occurrences, %d days, want 2 and 6 (Friday to Monday is one of four days)", occurrences, total)
	}
}
