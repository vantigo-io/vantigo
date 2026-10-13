package staff

import "time"

// This file is the overlap rule (design §3.5, plan Task 3), pure. It is
// judged under the person's lock against their approved, registered and
// requested rows read in it (plan reading 17), so two writes for one person
// are judged one after the other. A sykmelding stands outside the share rule
// over ferie — approved, which it leaves as it is until HR records the
// employee's demand to postpone it (ferieloven § 9, Task 5 review H1), or
// requested, whose approval is then refused (M2) — and over an egenmelding,
// which it trims (reading 24). Beside leave that allows a share a sykmelding
// of any degree stands while that leave leaves part of the day: the doctor
// grades the sick leave against the work that is left, so the sick share of
// the day is degree × (100 − leave %) / 100 (Task 5 review M1). Two
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
// shares of all of them that day sum to at most 100 % — but a sykmelding,
// which stands over ferie and egenmelding (sickOver) and beside leave
// allowing a share as long as that leave is under 100 % of the day. Hour
// rows overlap only hour rows, on their one day, by the hours summed to at
// most 24 (reading 27): an hour row counts no day, so it neither blocks nor
// is blocked by a day row.
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
		var leave int64 // the shares of the day's rows but a sykmelding
		sick := candidate.Kind == kindSickCertified
		if !sick {
			leave = candidate.Share
		}
		for _, e := range existing {
			if e.Hours > 0 || d.Before(e.First) || d.After(e.Last) || sickOver(candidate, e) {
				continue
			}
			switch {
			case e.Kind == kindSickCertified && sick:
				return true
			case e.Kind == kindSickCertified:
				if !shareable(candidate) {
					return true
				}
				sick = true
			case sick:
				if !shareable(e) {
					return true
				}
				leave += e.Share
			default:
				if !candidate.AllowsPercent || !e.AllowsPercent {
					return true
				}
				leave += e.Share
			}
		}
		if leave > wholeDay || (sick && leave >= wholeDay) {
			return true
		}
	}
	return false
}

// sickOver is whether candidate, a sykmelding, stands over e outside the
// share rule: e is ferie, approved or requested, or an egenmelding (trimmed).
func sickOver(candidate, e overlapRow) bool {
	return candidate.Kind == kindSickCertified && (e.Kind == kindHoliday || e.Kind == kindSickSelf)
}

// shareable is whether a sykmelding stands beside r by shares: r is a type
// that is not sick and allows a share.
func shareable(r overlapRow) bool { return !sickKind(r.Kind) && r.AllowsPercent }
