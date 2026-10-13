package staff

import (
	"slices"
	"time"
)

// This file is the sick-leave case's arithmetic (design S10, §4 The case,
// plan Task 5), pure. A case gathers a person's egenmelding and sykmelding
// rows: a row joins the case whose last day lies fewer than sixteen calendar
// days before its first — first_day ≤ last_day + 16 (ftrl. § 8-19 (3), plan
// pre-flight I1) — else it opens one. The arbeidsgiverperiode ends on the
// case's sixteenth sick day: every day of its rows, a graded day counting as
// one, and every day of a gap between two rows made only of Saturdays,
// Sundays and public holidays, which is bridged (R08-00 § 8-19, Revision 2
// Q7a, reading 30); a gap holding a working day is not bridged, and the days
// of it count nothing. The milestones are dated from the case's first day; a
// case closes when HR closes it or when sixteen days have passed since its
// last day — the join's own bound (reading 5). cases.go stores what this
// derives, under the person's lock.

// sickSpan is a sick row as a case reads it: its days, its id and kind, and
// the case it names today, nil for none.
type sickSpan struct {
	First, Last time.Time
	ID          int64
	Kind        string
	CaseID      *int64
}

// caseChain is the rows one case gathers, oldest first, and the days from
// the first's first to the last.
type caseChain struct {
	First, Last time.Time
	Spans       []sickSpan
}

// The milestone kinds (design §3.5), in their order.
const (
	milestoneEmployerPeriodEnd = "employer_period_end"
	milestoneFollowUpPlan      = "follow_up_plan"
	milestoneDialogue1         = "dialogue_1"
	milestoneNAVActivity       = "nav_activity"
	milestoneDialogue2         = "dialogue_2"
)

// milestoneKinds are every milestone of a case, in their order.
var milestoneKinds = []string{milestoneEmployerPeriodEnd, milestoneFollowUpPlan, milestoneDialogue1, milestoneNAVActivity, milestoneDialogue2}

// milestoneOffsets are the dated milestones' days from the case's first day
// (plan "Exact values"): the follow-up plan at four weeks (aml. § 4-6 (3)),
// the first dialogue meeting at seven, NAV's activity requirement at eight
// (ftrl. § 8-7a) and the second dialogue meeting at twenty-six.
var milestoneOffsets = map[string]int{
	milestoneFollowUpPlan: 28,
	milestoneDialogue1:    49,
	milestoneNAVActivity:  56,
	milestoneDialogue2:    182,
}

// The case's bounds in days: the join's and the close's sixteen, the sum the
// arbeidsgiverperiode ends on, the week a milestone is due within, and the
// 52-week estimate's 364.
const (
	caseJoinDays       = 16
	employerPeriodDays = 16
	milestoneDueWithin = 7
	maxBenefitDays     = 364
)

// joinsCase is whether a sick row from first joins a case whose last day is
// caseLast: fewer than sixteen days lie between.
func joinsCase(caseLast, first time.Time) bool {
	return !first.After(caseLast.AddDate(0, 0, caseJoinDays))
}

// chainsOf is spans gathered into cases by the join, each case's rows
// oldest first. The spans need not be sorted.
func chainsOf(spans []sickSpan) []caseChain {
	sorted := slices.Clone(spans)
	slices.SortStableFunc(sorted, func(a, b sickSpan) int { return a.First.Compare(b.First) })
	var out []caseChain
	for _, s := range sorted {
		if n := len(out); n > 0 && joinsCase(out[n-1].Last, s.First) {
			c := &out[n-1]
			c.Spans = append(c.Spans, s)
			if s.Last.After(c.Last) {
				c.Last = s.Last
			}
			continue
		}
		out = append(out, caseChain{First: s.First, Last: s.Last, Spans: []sickSpan{s}})
	}
	return out
}

// bridged is whether every day after from and before to is a day off — a
// Saturday, a Sunday or a public holiday (worksOn) — so the sickness on
// either side of it is one: an empty gap is bridged.
func bridged(from, to time.Time) bool {
	for d := from.AddDate(0, 0, 1); d.Before(to); d = d.AddDate(0, 0, 1) {
		if worksOn(d) {
			return false
		}
	}
	return true
}

// sickDays walks the days a case counts, in order, calling count for each
// until it answers false: every day of a row once, and every day of a
// bridged gap between two rows.
func sickDays(spans []sickSpan, count func(d time.Time) bool) {
	sorted := slices.Clone(spans)
	slices.SortStableFunc(sorted, func(a, b sickSpan) int { return a.First.Compare(b.First) })
	var cursor time.Time
	for i, s := range sorted {
		start := s.First
		if i > 0 && (!start.After(cursor) || bridged(cursor, start)) {
			start = cursor.AddDate(0, 0, 1)
		}
		for d := start; !d.After(s.Last); d = d.AddDate(0, 0, 1) {
			if !count(d) {
				return
			}
		}
		if i == 0 || s.Last.After(cursor) {
			cursor = s.Last
		}
	}
}

// employerPeriodEnd is the case's sixteenth sick day (sickDays), nil while
// the rows have not reached it.
func employerPeriodEnd(spans []sickSpan) *time.Time {
	var end *time.Time
	n := 0
	sickDays(spans, func(d time.Time) bool {
		n++
		if n == employerPeriodDays {
			end = &d
			return false
		}
		return true
	})
	return end
}

// milestoneDates is the dated milestones of a case from first.
func milestoneDates(first time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(milestoneOffsets))
	for kind, offset := range milestoneOffsets {
		out[kind] = first.AddDate(0, 0, offset)
	}
	return out
}

// milestoneDue is whether a milestone dated due is due (plan pre-flight I5):
// it has a date, the case is open, and the case has lasted to within a week
// of it — so a three-day case raises none.
func milestoneDue(open bool, last time.Time, due *time.Time) bool {
	return open && due != nil && !last.Before(due.AddDate(0, 0, -milestoneDueWithin))
}

// caseClosed is whether a case whose last day is last is closed on today: HR
// closed it, or sixteen days have passed since — a row from today would open
// a new case.
func caseClosed(byHR bool, last, today time.Time) bool {
	return byHR || !joinsCase(last, today)
}

// benefitEstimate is the case's 52-week end: the stored estimate, else
// first + 364 days (pre-flight M5).
func benefitEstimate(first time.Time, stored *time.Time) time.Time {
	if stored != nil {
		return *stored
	}
	return first.AddDate(0, 0, maxBenefitDays)
}
