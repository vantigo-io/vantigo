package staff

import "time"

// This file is the overlap rule (design §3.5, plan Task 3), pure. It is
// judged under the person's lock against their approved, registered and
// requested rows read in it (plan reading 17), so two writes for one person
// are judged one after the other. The two exceptions for sickness over
// approved ferie — a 100 % sykmelding splitting the ferie, a graded one
// standing beside it (S9, readings 24 and 25) — and a sykmelding over an
// egenmelding, which it trims (reading 24), stand outside the share rule;
// absences.go then splits or trims the row beneath (Task 5). A graded
// sykmelding takes its degree's share of a day beside a leave that allows a
// share (Task 3 review L5): 50 % sick on top of 50 % leave is one whole day,
// as a doctor grades the sick leave against the work left, while two
// sykmeldinger never share a day.

// overlapRow is a row as overlap reads it: its days, its hours (in
// hundredths, above zero for an hour row), whether its type allows a share,
// its share of each day (shareOf), and its type's kind and its status, which
// the sickness exceptions read.
type overlapRow struct {
	First, Last   time.Time
	Hours         int64
	AllowsPercent bool
	Share         int64
	Kind, Status  string
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
			if sickOver(candidate, e) {
				continue
			}
			if !sharesDay(candidate, e) {
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

// sickOver is whether candidate, a sykmelding, stands over e outside the
// share rule: e is approved ferie (split by a 100 % sykmelding, beside a
// graded one) or an egenmelding (trimmed).
func sickOver(candidate, e overlapRow) bool {
	if candidate.Kind != kindSickCertified {
		return false
	}
	return (e.Kind == kindHoliday && e.Status == absenceApproved) || e.Kind == kindSickSelf
}

// sharesDay is whether a and b may share a day by their shares: both allow a
// share, or one is a sykmelding — its degree its share — and the other a
// type that is not sick and allows one (L5).
func sharesDay(a, b overlapRow) bool {
	switch {
	case a.AllowsPercent && b.AllowsPercent:
		return true
	case a.Kind == kindSickCertified:
		return !sickKind(b.Kind) && b.AllowsPercent
	case b.Kind == kindSickCertified:
		return !sickKind(a.Kind) && a.AllowsPercent
	}
	return false
}
