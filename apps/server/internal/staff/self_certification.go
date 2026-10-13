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
// med", R08-00 § 8-24). Rows further apart are two occurrences, even within
// one case. The per-occurrence cap is judged on the run a new row joins; the
// rolling year's limit counts the earlier occurrences under the minimum
// policy, the days under the extended one, and both under custom. The limits
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

// rollingYear is the days a rolling year looks back over, today included.
const rollingYear = 365

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

// occurrencesOf is rows gathered into occurrences, oldest first: rows that
// overlap, touch or have only days off between them are one.
func occurrencesOf(rows []daySpan) []daySpan {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b daySpan) int { return a.First.Compare(b.First) })
	var out []daySpan
	for _, r := range sorted {
		if n := len(out); n > 0 && (!r.First.After(out[n-1].Last) || bridged(out[n-1].Last, r.First)) {
			if r.Last.After(out[n-1].Last) {
				out[n-1].Last = r.Last
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// daysWithin is the days of s from from to to, both counted.
func daysWithin(s daySpan, from, to time.Time) int {
	first, last := s.First, s.Last
	if first.Before(from) {
		first = from
	}
	if last.After(to) {
		last = to
	}
	return calendarDays(first, last)
}

// judgeSelfCert is what candidate, beside the person's existing egenmelding
// rows, goes past under l: "" when nothing.
func judgeSelfCert(l selfCertLimits, existing []daySpan, candidate daySpan) string {
	runs := occurrencesOf(append(slices.Clone(existing), candidate))
	var own daySpan
	for _, r := range runs {
		if !r.First.After(candidate.First) && !r.Last.Before(candidate.Last) {
			own = r
			break
		}
	}
	if calendarDays(own.First, own.Last) > l.DaysPerOccurrence {
		return selfCertOverOccurrence
	}
	if l.JudgeOccurrences {
		earlier := 0
		since := own.First.AddDate(0, 0, -rollingYear)
		for _, r := range runs {
			if r.First.Before(own.First) && !r.Last.Before(since) {
				earlier++
			}
		}
		if earlier >= l.OccurrencesPerYear {
			return selfCertOverOccurrences
		}
	}
	if l.JudgeDays {
		to := candidate.Last
		from := to.AddDate(0, 0, 1-rollingYear)
		total := 0
		for _, r := range runs {
			total += daysWithin(r, from, to)
		}
		if total > l.DaysPerYear {
			return selfCertOverDays
		}
	}
	return ""
}

// selfCertUsed is the occurrences and the days of rows in the rolling year
// ending today: an occurrence touching it counts, and its days within it.
func selfCertUsed(rows []daySpan, today time.Time) (occurrences, days int) {
	from := today.AddDate(0, 0, 1-rollingYear)
	for _, r := range occurrencesOf(rows) {
		if n := daysWithin(r, from, today); n > 0 {
			occurrences++
			days += n
		}
	}
	return occurrences, days
}
