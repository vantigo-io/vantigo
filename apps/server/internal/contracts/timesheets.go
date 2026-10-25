package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Timesheets is the hours logged per identity user and week, as the module
// that owns the hours reports them: a read-only, in-process port a module
// showing a person's at-a-glance figure (staff) reads a week's total
// through, without importing the owning module (barred by depguard) or
// reading its PostgreSQL schema (barred by internal/db/schema_test.go).
// Time is its sole provider (Module.Timesheets); Compose wires it into every
// module's Deps before any Mount runs. It is nil when time is disabled,
// which a consumer reads as "time tracking is off" — never as "nothing was
// logged".
//
// It performs no authorization, as ProjectActuals performs none: it is
// consumer-gated. The rule that decides who may see whose hours on a
// person's page is the consumer's (Staff's manager rule), which the provider
// cannot know, so the consumer has decided before it calls, and calls with
// the request's context, never inside a transaction of its own.
//
// A provider fails rather than under-reports: an error means "the hours
// could not be read", never "there are none".
type Timesheets interface {
	// WeekHours answers, for each user id, the hours of the week starting on
	// weekStart — the caller passes a Monday, a calendar date at midnight
	// UTC; the provider reads the seven days from it whatever weekday it is.
	// Logged is every entry not rejected (drafts, submitted, approved and
	// invoiced), Approved the approved and invoiced ones. A user with no
	// entry in the week is absent from the map; no user ids answer an empty
	// map.
	WeekHours(ctx context.Context, weekStart time.Time, userIDs []uuid.UUID) (map[uuid.UUID]WeekHours, error)
}

// WeekHours is one user's hours in one week, in hours.
type WeekHours struct {
	Logged   float64
	Approved float64
}
