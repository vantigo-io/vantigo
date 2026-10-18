package contracts

import (
	"context"
	"time"
)

// BusinessCalendar is the installation's calendar: the one time zone every
// bookkeeping day is judged in — an invoice's issue day, a period lock, a
// trip's departure day, Staff's "today" — and the two derivations each module
// made for itself until the time zones design (docs/superpowers/specs/
// 2026-10-19-time-zones-design.md, D2) made them one. identity implements it
// over the workspace's system settings; Compose and Workers wire it into
// every module's Deps, and it is always set once composed, like
// UserDirectory, because identity is always mounted.
//
// A date column is a civil date and is never converted; the zone enters only
// when a day is derived from an instant, which is what these three answer.
// Nothing here is cached across requests: a changed zone applies to the next
// request. A module that needs the zone more than once in a request calls
// Zone once and keeps the *time.Location.
type BusinessCalendar interface {
	// Zone is the business time zone. A stored name the server cannot load
	// (written by hand, or dropped by a Go release) answers UTC rather than
	// failing every request; setting the zone again is the way back.
	Zone(ctx context.Context) (*time.Location, error)
	// Today is the business calendar day of now, as a date at midnight UTC —
	// the form every date column is compared at (holidays.DayIn).
	Today(ctx context.Context, now time.Time) (time.Time, error)
	// Day is the business calendar day an instant falls on, in the same form.
	Day(ctx context.Context, t time.Time) (time.Time, error)
}
