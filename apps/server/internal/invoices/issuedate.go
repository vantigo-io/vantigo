package invoices

import "time"

// This file is D6's issue-date rule, as a pure function: which dates a
// document may be issued with on a given day.
//
// Lovdata § 5-1-3 third paragraph allows exactly one date other than the
// actual one: a document issued within the first fifteen working days of a
// month may carry the last day of the previous month, provided the goods or
// the service were delivered by then. "Virkedager" is not defined in the
// regulation, and fifteen working days always reach at least the 17th, even
// counting Saturdays; "calendar day ≤ 15" is therefore always within the law,
// needs no holiday calendar, and is stricter than the law — docs/invoices.md
// says so. On top of that no date may be before the latest issue date of any
// issued document, so numbers and dates are both monotone: an extra guard the
// law does not ask for.

// allowedIssueDates are the dates a document delivered by deliveryEnd may be
// issued with today, the earliest first. latest is the latest issue date of
// any issued document; a zero deliveryEnd (no delivery yet) allows only today.
func allowedIssueDates(today, deliveryEnd, latest time.Time) []time.Time {
	var allowed []time.Time
	lastOfPrevious := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	if today.Day() <= 15 && !deliveryEnd.IsZero() && !deliveryEnd.After(lastOfPrevious) &&
		(latest.IsZero() || !lastOfPrevious.Before(latest)) {
		allowed = append(allowed, lastOfPrevious)
	}
	if latest.IsZero() || !today.Before(latest) {
		allowed = append(allowed, today)
	}
	return allowed
}

// issuedLate is § 5-2-2's "senest en måned etter levering", as a warning: the
// document is issued after the day one month after its delivery ended (D6). It
// never refuses — refusing would leave the sale undocumented.
func issuedLate(issueDate, deliveryEnd time.Time) bool {
	return !deliveryEnd.IsZero() && issueDate.After(oneMonthAfter(deliveryEnd))
}

// oneMonthAfter is the same day of the next month, clamped to that month's
// last day: 31 January gives 28 (or 29) February. time.AddDate would normalise
// 31 February into March and move the deadline past the month the law gives.
func oneMonthAfter(d time.Time) time.Time {
	first := time.Date(d.Year(), d.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(d.Day(), last), 0, 0, 0, 0, time.UTC)
}
