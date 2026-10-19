package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// CalendarSource is a module's view of its own rows as calendar items
// (calendar design D1). A module that has time-bound rows about a person fills
// the slot (module.Module.CalendarSource); the calendar module reads every
// filled one, enabled modules only (module.Deps.CalendarSources), and never a
// module's table.
//
// Each provider is its own gatekeeper: the visibility rule is the provider's,
// already a query its module owns, and the calendar never re-states it. A
// provider that fails fails the whole request — absent must never also mean
// "could not be read".
type CalendarSource interface {
	// CalendarKinds names the kinds this provider may answer, so the calendar
	// can list its sources — for the filter, the legend and the colours —
	// without a query. Stable keys, "<module>.<noun>": "customers.follow-up".
	CalendarKinds() []CalendarKind
	// CalendarItems answers the caller's items in the window. The provider
	// applies its own visibility rule to the principal in ctx and answers
	// nothing — not an error — when the caller lacks its permission; the
	// calendar shows what comes back and never says what was withheld. Called
	// with the request's context, never inside a transaction of the caller's.
	CalendarItems(ctx context.Context, q CalendarQuery) ([]CalendarItem, error)
}

// CalendarKind is one kind of item a CalendarSource may answer.
type CalendarKind struct {
	Key    string // "projects.task"
	Module string // "projects"
}

// CalendarQuery is the window a CalendarSource is asked about, for one user.
type CalendarQuery struct {
	UserID uuid.UUID
	From   time.Time      // the first day, a civil date at midnight UTC, inclusive
	To     time.Time      // the last day, the same form, inclusive
	Zone   *time.Location // the viewer's zone, for a provider that buckets an instant
}

// CalendarItem is one row of a provider's, as the calendar draws it.
type CalendarItem struct {
	Kind     string // one of the provider's CalendarKinds
	ID       string // stable within the kind: the row's id
	Title    string
	Subtitle string // the customer's name, the project's code — one line, may be empty
	AllDay   bool
	// An all-day item spans civil dates, EndDate inclusive, both at midnight UTC.
	StartDate, EndDate time.Time
	// A timed item spans instants, EndsAt exclusive. Zero when AllDay.
	StartsAt, EndsAt time.Time
	Done             bool   // a follow-up done, a task done, an entry approved: rendered struck
	Path             string // the SPA route of the record, "/customers/42", for the link back
}
