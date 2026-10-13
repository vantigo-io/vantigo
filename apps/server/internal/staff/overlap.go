package staff

import "time"

// This file is the overlap rule (design §3.5, plan Task 3), pure. It is
// judged under the person's lock against their approved, registered and
// requested rows read in it (plan reading 17), so two writes for one person
// are judged one after the other. The two exceptions for sickness over
// approved ferie — a 100 % sykmelding splitting the ferie, a graded one
// standing beside it (S9, readings 24 and 25) — are delivery B's Task 5's:
// they belong where this rule refuses two day rows that do not both allow a
// share, and until then they are refused like any other overlap.

// overlapRow is a row as overlap reads it: its days, its hours (in
// hundredths, above zero for an hour row), whether its type allows a share
// and its share of each day (shareOf).
type overlapRow struct {
	First, Last   time.Time
	Hours         int64
	AllowsPercent bool
	Share         int64
}

// hoursInADay is 24 hours in hundredths.
const hoursInADay = 2400

// overlapRefused is whether candidate may not stand beside existing
// (absence_overlap). Day rows: on every day candidate covers, each existing
// day row that covers it must allow a share as candidate must, and the
// shares of all of them that day sum to at most 100 %. Hour rows overlap
// only hour rows, on their one day, by the hours summed to at most 24
// (reading 27): an hour row counts no day, so it neither blocks nor is
// blocked by a day row.
func overlapRefused(candidate overlapRow, existing []overlapRow) bool {
	if candidate.Hours > 0 {
		sum := candidate.Hours
		for _, e := range existing {
			if e.Hours > 0 && e.First.Equal(candidate.First) {
				sum += e.Hours
			}
		}
		return sum > hoursInADay
	}
	for d := candidate.First; !d.After(candidate.Last); d = d.AddDate(0, 0, 1) {
		sum := candidate.Share
		for _, e := range existing {
			if e.Hours > 0 || d.Before(e.First) || d.After(e.Last) {
				continue
			}
			if !candidate.AllowsPercent || !e.AllowsPercent {
				return true
			}
			sum += e.Share
		}
		if sum > wholeDay {
			return true
		}
	}
	return false
}
