package staff

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
)

// This file is the module's days (design §4 Days and the zone), pure: the
// working days a holiday or a leave deducts, counted by the platform
// calendar's public holidays (internal/holidays), never a list of the
// module's own; the calendar days egenmelding and the arbeidsgiverperiode
// count; and a row's share of each day. Days are civil dates at UTC
// midnight, the calendar's convention.

// worksOn is whether a person works on d: the five-day week, Monday to
// Friday, without Norway's public holidays. It is the one function a
// part-time working pattern — a phase 2 column — would read through (spec
// reading 4).
func worksOn(d time.Time) bool { return holidays.BusinessDay(d) }

// workingDays is the working days from from to to, both counted (worksOn);
// none when to is before from.
func workingDays(from, to time.Time) int {
	n := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if worksOn(d) {
			n++
		}
	}
	return n
}

// calendarDays is every day from from to to, both counted; none when to is
// before from.
func calendarDays(from, to time.Time) int {
	if to.Before(from) {
		return 0
	}
	return int(to.Sub(from).Hours()/24) + 1
}

// The sick kinds and child_sick (design S8, S10, S11): the kinds whose rows
// are health data, class E (§3.5), and that take no note.
const (
	kindHoliday       = "holiday"
	kindSickSelf      = "sick_self"
	kindSickCertified = "sick_certified"
	kindChildSick     = "child_sick"
)

// sickKind is whether kind is a sick kind: egenmelding or sykmelding.
func sickKind(kind string) bool { return kind == kindSickSelf || kind == kindSickCertified }

// healthKind is whether a row of kind is health data (design §3.5): a sick
// kind, or a child's sickness.
func healthKind(kind string) bool { return sickKind(kind) || kind == kindChildSick }

// wholeDay is a whole day's share, 100.00 % in hundredths of a percent.
const wholeDay = 10000

// shareOf is the share of each day a row of kind takes, in hundredths of a
// percent (plan pre-flight I4): its percent, except on a sick kind, whose
// percent is 100 and whose share is its degree — a whole day when it names
// none.
func shareOf(kind string, percent, degree pgtype.Numeric) int64 {
	if sickKind(kind) {
		if !degree.Valid {
			return wholeDay
		}
		return hundredthsOfNumeric(degree)
	}
	if !percent.Valid {
		return wholeDay
	}
	return hundredthsOfNumeric(percent)
}
