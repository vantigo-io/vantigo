package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// CalendarBusy is the calendar's busy time of people as it offers it to
// another module (calendar Outlook sync design O9.8): today Planning's
// board, which shows each person's busy hours and days a week beside their
// bookings. It is the first contract the calendar provides, and a
// single-provider slot: the calendar fills it through
// module.Module.CalendarBusy, and Compose resolves the enabled module's into
// module.Deps.CalendarBusy, refusing two. nil when the calendar is off.
//
// It is provider-gated (module boundaries' rule 12): the calendar answers
// the principal of the request in ctx — their own user id under
// calendar:access, anyone else's under calendar:view-colleagues, the
// calendar's own colleague rule, which a consumer cannot know — and nothing,
// not an error, for an id the caller may not see, or for every id when ctx
// carries no request. A consumer shows what comes back and never says what
// was withheld; an error is a failure, never an answer. It is called with
// the request's context and never inside a transaction.
//
// A user's busy time is the appointments a colleague's calendar shows as
// busy — owned or accepted, busy, not cancelled, each occurrence — and,
// while the user's Outlook sync is on, the busy and out-of-office events of
// their mailbox's default calendar the sync read (design O9.3): timed ones
// as merged instants, all-day ones as merged date spans.
//
// The figures are shown, not subtracted: a meeting about a project is often
// the booking's own time, so whatever availability a consumer computes stays
// its own and the busy time stands beside it — that is the consumer's
// concern, which the contract only names.
type CalendarBusy interface {
	// Busy answers the busy time of userIDs — at most CalendarBusyMaxUsers
	// — over from..to, civil dates at midnight UTC, both inclusive, at most
	// CalendarBusyMaxDays days (ErrBusyWindowOutOfBounds for a wider or an
	// inverted ask). A user the caller may not see, or who is busy at no
	// time in the window, is not in the map.
	Busy(ctx context.Context, userIDs []uuid.UUID, from, to time.Time) (map[uuid.UUID]BusyTime, error)
}

// The bounds of a Busy call.
const (
	// CalendarBusyMaxUsers is the most users one Busy call asks for.
	CalendarBusyMaxUsers = 500
	// CalendarBusyMaxDays is the most days one Busy call's window spans.
	CalendarBusyMaxDays = 400
)

// BusyTime is one user's busy time in the window asked, merged: no two
// spans, nor two date spans, overlap or touch.
type BusyTime struct {
	// Spans are the timed busy instants, ordered, clipped to the window.
	Spans []TimeSpan
	// Days are the all-day busy dates, ordered, clipped to the window.
	Days []DateSpan
}

// TimeSpan is a run of instants, StartsAt..EndsAt, the end exclusive.
type TimeSpan struct {
	StartsAt, EndsAt time.Time
}

// ErrBusyWindowOutOfBounds: a Busy call asked for more than
// CalendarBusyMaxUsers users or CalendarBusyMaxDays days, or a window that
// ends before it starts.
var ErrBusyWindowOutOfBounds = errors.New("contracts: the busy window is out of bounds")
