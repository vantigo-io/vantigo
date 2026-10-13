package staff

import (
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is what one working day of a person's absence counts as (plan
// Task 6), pure: the payroll file's totals per kind and the sick-leave rate
// read it, so the two never count a day differently. Every share is in
// hundredths of a percent of the day (wholeDay is the whole of it).
//
//   - Sick days by their degree (pre-flight I4) — an egenmelding a whole day
//     — and, beside leave the same day, degree × (100 − the leaves'
//     percent) / 100, the leaves summed and capped at a whole day, so a day of
//     100 % leave counts no sickness (Revision 3 M1).
//   - Approved ferie is a holiday day and counts no sickness — no
//     sykepenger are paid on a holiday day, and an egenmelding cannot
//     interrupt one — except under a 100 % sykmelding, which counts the day
//     once, as sick, and not as holiday, until a recorded postponement splits
//     the ferie (Revision 3 H1).
//   - Child-sick days, leave and other day rows by their percent.

// dayShares is one working day's absence, per kind.
type dayShares struct {
	Holiday, SelfSick, CertifiedSick, ChildSick, Leave, Other int64
}

// sick is the day's sick share, egenmelding and sykmelding together.
func (s dayShares) sick() int64 { return s.SelfSick + s.CertifiedSick }

// covers is whether row r is a day row covering d.
func covers(r store.AbsencesBetweenRow, d time.Time) bool {
	return r.Unit != "hours" && !civil(r.FirstDay).After(d) && !civil(r.LastDay).Before(d)
}

// sharesOn is d's shares from rows, the person's approved and registered
// rows; rows not covering d count nothing.
func sharesOn(rows []store.AbsencesBetweenRow, d time.Time) dayShares {
	var beside int64
	wholeSick, holiday := false, false
	var on []store.AbsencesBetweenRow
	for _, r := range rows {
		if !covers(r, d) {
			continue
		}
		on = append(on, r)
		share := shareOf(r.Kind, r.Percent, r.DegreePercent)
		switch {
		case r.Kind == kindSickCertified && share >= wholeDay:
			wholeSick = true
		case r.Kind == kindHoliday:
			holiday = true
		case !healthKind(r.Kind):
			beside += share
		}
	}
	beside = min(beside, wholeDay)
	// A holiday day under less than a 100 % sykmelding is a holiday day.
	sickCounts := wholeSick || !holiday
	var out dayShares
	for _, r := range on {
		share := shareOf(r.Kind, r.Percent, r.DegreePercent)
		switch r.Kind {
		case kindHoliday:
			if !wholeSick {
				out.Holiday += share
			}
		case kindSickSelf:
			if sickCounts {
				out.SelfSick += share * (wholeDay - beside) / wholeDay
			}
		case kindSickCertified:
			if sickCounts {
				out.CertifiedSick += share * (wholeDay - beside) / wholeDay
			}
		case kindChildSick:
			out.ChildSick += share
		case kindLeave:
			out.Leave += share
		default:
			out.Other += share
		}
	}
	return out
}

// kindLeave is the leave kind (design S8): what a-melding leave rows are made of.
const kindLeave = "leave"

// kindTimeOff is time off in lieu, counted in hours.
const kindTimeOff = "time_off"
