package staff

import (
	"slices"
	"time"
)

// This file is egenmelding's counters (design S10, §4 Egenmelding, plan
// Task 5), pure. An occurrence is a maximal run of contiguous egenmelding
// days: rows that touch, or that only Saturdays, Sundays and public holidays
// lie between, are one (plan pre-flight I2, Revision 2 Q7a), counted in
// calendar days with the bridged days included ("også arbeidsfrie dager tas
// med", R08-00 § 8-24). Days a sykmelding covers join the egenmelding on
// either side too — one illness is one occurrence (Task 5 review M4) — but
// count no self-certified day. Rows further apart are two occurrences, even
// within one case. The per-occurrence cap is judged on the run a new row
// joins; the rolling year's limit — in every rolling year the new row lies
// in, later occurrences counted too, for a backdated row (review L10) —
// counts the occurrences under the minimum policy, the days under the
// extended one, and both under custom. The limits
// key on the policy's name: under minimum and extended the preset's numbers
// are used whatever the settings row holds, and its counters are read under
// custom only (Task 2 review).

// daySpan is an egenmelding row's days, or an occurrence's.
type daySpan struct{ First, Last time.Time }

// selfCertLimits is what a policy allows: the days of one occurrence, the
// occurrences and the days in a rolling year, and which of the two yearly
// limits it judges.
type selfCertLimits struct {
	DaysPerOccurrence, OccurrencesPerYear, DaysPerYear int
	JudgeOccurrences, JudgeDays                        bool
}

// What a refused egenmelding went past: the days of its occurrence, the
// occurrences of a rolling year, the days of a rolling year.
const (
	selfCertOverOccurrence  = "occurrence"
	selfCertOverOccurrences = "occurrences"
	selfCertOverDays        = "days"
)

// The presets' numbers (design S10, research §4.5): the statutory minimum,
// ftrl. § 8-23 — three calendar days, four times in twelve months — and the
// extended scheme an IA-style agreement gives — eight days at a time, 24 in
// twelve months.
const (
	minimumDaysPerOccurrence  = 3
	minimumOccurrencesPerYear = 4
	extendedDaysPerOccurrence = 8
	extendedDaysPerYear       = 24
)

// windowFrom is the first day of the twelve months ending on to, to
// included (ftrl. § 8-23 "i løpet av 12 måneder"; whole-branch review): the
// day after the same date a year earlier, so a window across 29 February is
// 366 days long.
func windowFrom(to time.Time) time.Time { return to.AddDate(-1, 0, 1) }

// limitsOf is what policy p allows, by its name.
func limitsOf(p selfCertPolicy) selfCertLimits {
	switch p.Policy {
	case selfCertMinimum:
		return selfCertLimits{DaysPerOccurrence: minimumDaysPerOccurrence, OccurrencesPerYear: minimumOccurrencesPerYear, JudgeOccurrences: true}
	case selfCertExtended:
		return selfCertLimits{DaysPerOccurrence: extendedDaysPerOccurrence, DaysPerYear: extendedDaysPerYear, JudgeDays: true}
	}
	return selfCertLimits{
		DaysPerOccurrence: int(p.DaysPerOccurrence), OccurrencesPerYear: int(p.OccurrencesPerYear), DaysPerYear: int(p.DaysPerYear),
		JudgeOccurrences: true, JudgeDays: true,
	}
}

// covered is whether d lies in one of spans.
func covered(spans []daySpan, d time.Time) bool {
	for _, s := range spans {
		if !d.Before(s.First) && !d.After(s.Last) {
			return true
		}
	}
	return false
}

// bridgedBy is whether every day after from and before to is a day off or
// one sick covers.
func bridgedBy(from, to time.Time, sick []daySpan) bool {
	for d := from.AddDate(0, 0, 1); d.Before(to); d = d.AddDate(0, 0, 1) {
		if worksOn(d) && !covered(sick, d) {
			return false
		}
	}
	return true
}

// occurrencesOf is rows gathered into occurrences, oldest first: rows that
// overlap, touch or have only days off and sykmelding days between them are
// one.
func occurrencesOf(rows, sick []daySpan) []daySpan {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b daySpan) int { return a.First.Compare(b.First) })
	var out []daySpan
	for _, r := range sorted {
		if n := len(out); n > 0 && (!r.First.After(out[n-1].Last) || bridgedBy(out[n-1].Last, r.First, sick)) {
			if r.Last.After(out[n-1].Last) {
				out[n-1].Last = r.Last
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// daysWithin is the self-certified days of s from from to to, both counted:
// its calendar days but those a sykmelding covers.
func daysWithin(s daySpan, from, to time.Time, sick []daySpan) int {
	first, last := s.First, s.Last
	if first.Before(from) {
		first = from
	}
	if last.After(to) {
		last = to
	}
	n := 0
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		if !covered(sick, d) {
			n++
		}
	}
	return n
}

// judgeSelfCert is what candidate, beside the person's existing egenmelding
// rows and their sykmelding days sick, goes past under l: "" when nothing.
func judgeSelfCert(l selfCertLimits, existing []daySpan, candidate daySpan, sick []daySpan) string {
	runs := occurrencesOf(append(slices.Clone(existing), candidate), sick)
	var own daySpan
	for _, r := range runs {
		if !r.First.After(candidate.First) && !r.Last.Before(candidate.Last) {
			own = r
			break
		}
	}
	if daysWithin(own, own.First, own.Last, sick) > l.DaysPerOccurrence {
		return selfCertOverOccurrence
	}
	// Every twelve months holding a day of the candidate: each window ends
	// on a day from the candidate's first to twelve months after its last.
	for to := candidate.First; !windowFrom(to).After(candidate.Last); to = to.AddDate(0, 0, 1) {
		from := windowFrom(to)
		occurrences, total := 0, 0
		for _, r := range runs {
			if n := daysWithin(r, from, to, sick); n > 0 {
				occurrences++
				total += n
			}
		}
		if l.JudgeOccurrences && occurrences > l.OccurrencesPerYear {
			return selfCertOverOccurrences
		}
		if l.JudgeDays && total > l.DaysPerYear {
			return selfCertOverDays
		}
	}
	return ""
}

// selfCertUsed is the occurrences and the self-certified days of rows in the
// rolling year ending today: an occurrence touching it counts, and its days
// within it.
func selfCertUsed(rows, sick []daySpan, today time.Time) (occurrences, days int) {
	from := windowFrom(today)
	for _, r := range occurrencesOf(rows, sick) {
		if n := daysWithin(r, from, today, sick); n > 0 {
			occurrences++
			days += n
		}
	}
	return occurrences, days
}
