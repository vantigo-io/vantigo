package timetracking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the module's contracts.Timesheets (staff person-glance design
// §3.2): a user's hours in one week, for the module that shows a person's
// at-a-glance figure. Like actuals.go it authorizes nothing — the consumer
// has decided who may see whose hours — and it serves from this module's own
// table and nothing else.

// timesheets is this module's contracts.Timesheets: read-only, holding
// nothing but the queries. Compose builds it once, before any module mounts.
type timesheets struct{ q *store.Queries }

var _ contracts.Timesheets = (*timesheets)(nil)

// newTimesheets is Module's Timesheets: the constructor Compose calls with
// the dependencies it was given. It takes the pool and nothing else.
func newTimesheets(d module.Deps) contracts.Timesheets {
	return &timesheets{q: store.New(d.Pool)}
}

// WeekHours sums each user's entries dated in the seven days from weekStart.
// The caller passes a Monday; any other date is still read as the seven days
// from it. No user ids answer an empty map without a query.
func (t *timesheets) WeekHours(ctx context.Context, weekStart time.Time, userIDs []uuid.UUID) (map[uuid.UUID]contracts.WeekHours, error) {
	out := map[uuid.UUID]contracts.WeekHours{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := t.q.WeekHoursOf(ctx, store.WeekHoursOfParams{UserIds: userIDs, WeekStart: pgDate(weekStart)})
	if err != nil {
		return nil, fmt.Errorf("time: sum the users' week: %w", err)
	}
	for _, r := range rows {
		logged, err := centsFromNumeric(r.Logged)
		if err != nil {
			return nil, err
		}
		approved, err := centsFromNumeric(r.Approved)
		if err != nil {
			return nil, err
		}
		out[r.UserID] = contracts.WeekHours{Logged: float64(logged) / 100, Approved: float64(approved) / 100}
	}
	return out, nil
}
