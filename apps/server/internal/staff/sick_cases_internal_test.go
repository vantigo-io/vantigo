package staff

import (
	"testing"
	"time"
)

// span is a sick row's days.
func span(t *testing.T, first, last string) sickSpan {
	t.Helper()
	return sickSpan{First: day(t, first), Last: day(t, last)}
}

// The case join (ftrl. § 8-19 (3), plan pre-flight I1): a sick row joins a
// case when fewer than sixteen calendar days lie between the case's last day
// and its own first — first_day ≤ last_day + 16. Last day 1 January: a row
// from 17 January joins (2–16 January between, fifteen days), one from 18
// January opens a new case.
func TestCaseJoin(t *testing.T) {
	t.Parallel()
	last := day(t, "2027-01-01")
	for _, c := range []struct {
		first string
		joins bool
	}{
		{"2027-01-02", true},
		{"2027-01-17", true},
		{"2027-01-18", false},
		{"2027-02-01", false},
	} {
		if got := joinsCase(last, day(t, c.first)); got != c.joins {
			t.Errorf("a row from %s after a case ending 2027-01-01: joins = %v, want %v", c.first, got, c.joins)
		}
	}
	chains := chainsOf([]sickSpan{span(t, "2026-12-28", "2027-01-01"), span(t, "2027-01-17", "2027-01-18"), span(t, "2027-02-04", "2027-02-05")})
	if len(chains) != 2 || len(chains[0].Spans) != 2 || len(chains[1].Spans) != 1 {
		t.Fatalf("chains = %+v, want the first two rows together and the third alone (17 days after 18 January)", chains)
	}
	if !chains[0].First.Equal(day(t, "2026-12-28")) || !chains[0].Last.Equal(day(t, "2027-01-18")) {
		t.Errorf("the first chain = %s..%s, want 2026-12-28..2027-01-18", chains[0].First, chains[0].Last)
	}
}

// The arbeidsgiverperiode (ftrl. § 8-19, R08-00 § 8-19, Revision 2 Q7a)
// ends on the case's sixteenth sick day: the rows' days, a graded day
// counting as one, plus the days of a gap between two rows made only of
// Saturdays, Sundays and public holidays — such a gap is bridged and its days
// counted; a gap holding a working day is not, and none of its days count.
// Until the sixteenth day is reached there is no end.
func TestEmployerPeriodEnd(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		spans []sickSpan
		want  string // "" for none yet
	}{
		{"sixteen days in one row", []sickSpan{span(t, "2026-10-05", "2026-10-25")}, "2026-10-20"},
		{"egenmelding Monday–Friday and a sykmelding from the next Monday end on the Tuesday of week 3, the weekend bridged",
			[]sickSpan{span(t, "2026-10-05", "2026-10-09"), span(t, "2026-10-12", "2026-10-30")}, "2026-10-20"},
		{"a working Thursday between is not bridged: its day does not count",
			[]sickSpan{span(t, "2026-10-05", "2026-10-07"), span(t, "2026-10-09", "2026-10-30")}, "2026-10-21"},
		{"Easter's Thursday to Monday between two rows is bridged",
			[]sickSpan{span(t, "2026-03-23", "2026-04-01"), span(t, "2026-04-07", "2026-04-10")}, "2026-04-07"},
		{"an interrupted case: the worked days between are not counted",
			[]sickSpan{span(t, "2026-10-05", "2026-10-07"), span(t, "2026-10-14", "2026-10-28")}, "2026-10-26"},
		// The period starts on a working day (NAV; whole-branch review): a
		// row that begins on a Saturday counts from the Monday, and so does a
		// row after an unbridged gap.
		{"a row from a Saturday counts from the Monday", []sickSpan{span(t, "2026-10-03", "2026-10-25")}, "2026-10-20"},
		{"after worked days, a row from a Saturday counts from the Monday",
			[]sickSpan{span(t, "2026-10-05", "2026-10-07"), span(t, "2026-10-10", "2026-10-30")}, "2026-10-24"},
		{"fifteen days: no end yet", []sickSpan{span(t, "2026-10-05", "2026-10-19")}, ""},
		{"a three-day case", []sickSpan{span(t, "2026-10-05", "2026-10-07")}, ""},
	} {
		got := employerPeriodEnd(c.spans)
		switch {
		case c.want == "" && got != nil:
			t.Errorf("%s: end = %s, want none yet", c.name, got.Format(time.DateOnly))
		case c.want != "" && (got == nil || !got.Equal(day(t, c.want))):
			t.Errorf("%s: end = %v, want %s", c.name, got, c.want)
		}
	}
}

// The milestones (plan "Exact values") are dated from the case's first day:
// the follow-up plan at four weeks, the first dialogue meeting at seven, NAV's
// activity requirement at eight and the second dialogue meeting at
// twenty-six. One is due only while the case is open and has lasted to within
// a week of it (plan pre-flight I5): a three-day case has none due.
func TestMilestones(t *testing.T) {
	t.Parallel()
	first := day(t, "2026-10-05")
	dates := milestoneDates(first)
	for kind, want := range map[string]string{
		milestoneFollowUpPlan: "2026-11-02",
		milestoneDialogue1:    "2026-11-23",
		milestoneNAVActivity:  "2026-11-30",
		milestoneDialogue2:    "2027-04-05",
	} {
		if got, ok := dates[kind]; !ok || !got.Equal(day(t, want)) {
			t.Errorf("%s = %v, want %s", kind, got, want)
		}
	}
	plan := dates[milestoneFollowUpPlan]
	for _, c := range []struct {
		name string
		open bool
		last string
		due  bool
	}{
		{"a three-day case", true, "2026-10-07", false},
		{"a case lasting to a week before", true, "2026-10-26", true},
		{"one day short of that", true, "2026-10-25", false},
		{"a long case, closed", false, "2026-11-20", false},
	} {
		if got := milestoneDue(c.open, false, day(t, c.last), &plan); got != c.due {
			t.Errorf("%s: the follow-up plan due = %v, want %v", c.name, got, c.due)
		}
	}
	if milestoneDue(true, false, day(t, "2026-12-31"), nil) {
		t.Error("a milestone with no date is due")
	}
	// Done or waived, it is due no more (review L7).
	if milestoneDue(true, true, day(t, "2026-10-26"), &plan) {
		t.Error("a milestone done or waived is still due")
	}
}

// A case is closed when HR closed it or when no row has joined for sixteen
// days past its last — the join's own bound (plan reading 5); the 52-week
// estimate is first_day + 364 until edited (pre-flight M5).
func TestCaseClosedAndEstimate(t *testing.T) {
	t.Parallel()
	last := day(t, "2027-01-01")
	if caseClosed(false, last, day(t, "2027-01-17")) {
		t.Error("closed on 17 January, when a row from then would still join")
	}
	if !caseClosed(false, last, day(t, "2027-01-18")) {
		t.Error("open on 18 January, when a row from then opens a new case")
	}
	if !caseClosed(true, last, day(t, "2027-01-02")) {
		t.Error("open although HR closed it")
	}
	if got := benefitEstimate(day(t, "2026-10-05"), nil); !got.Equal(day(t, "2027-10-04")) {
		t.Errorf("the derived estimate = %s, want 2027-10-04", got.Format(time.DateOnly))
	}
	edited := day(t, "2027-09-01")
	if got := benefitEstimate(day(t, "2026-10-05"), &edited); !got.Equal(edited) {
		t.Errorf("the edited estimate = %s, want 2027-09-01", got.Format(time.DateOnly))
	}
}
