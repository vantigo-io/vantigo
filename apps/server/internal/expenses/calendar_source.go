package expenses

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// calendarKindClaim is the one kind this module projects onto the calendar
// (calendar design D1).
const calendarKindClaim = "expenses.claim"

// calendarSource is this module's contracts.CalendarSource (calendar design
// D1): the caller's own travel claims, each a block from departure to return.
// It is its own gatekeeper: expenses:access, the key that already reads a
// person's own claims; expenses:view-all widens nothing, every row being the
// caller's. Without the key it answers nothing, not an error.
type calendarSource struct {
	q      *store.Queries
	access contracts.Access
}

var _ contracts.CalendarSource = (*calendarSource)(nil)

// newCalendarSource is Module's CalendarSource.
func newCalendarSource(d module.Deps) contracts.CalendarSource {
	return &calendarSource{q: store.New(d.Pool), access: d.Access}
}

func (s *calendarSource) CalendarKinds() []contracts.CalendarKind {
	return []contracts.CalendarKind{{Key: calendarKindClaim, Module: "expenses"}}
}

// CalendarItems judges the window in the viewer's zone: a journey is two
// instants, so the window is too — the first day's midnight there, and the
// midnight after the last day.
func (s *calendarSource) CalendarItems(ctx context.Context, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	if !contracts.HasPermission(ctx, s.access, "expenses:access") {
		return nil, nil
	}
	zone := q.Zone
	if zone == nil {
		zone = time.UTC
	}
	rows, err := s.q.CalendarClaims(ctx, store.CalendarClaimsParams{
		UserID:      q.UserID,
		WindowStart: time.Date(q.From.Year(), q.From.Month(), q.From.Day(), 0, 0, 0, 0, zone),
		WindowEnd:   time.Date(q.To.Year(), q.To.Month(), q.To.Day()+1, 0, 0, 0, 0, zone),
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: calendar claims: %w", err)
	}
	items := make([]contracts.CalendarItem, 0, len(rows))
	for _, r := range rows {
		item := contracts.CalendarItem{
			Kind:     calendarKindClaim,
			ID:       strconv.FormatInt(r.ID, 10),
			Title:    r.Purpose,
			StartsAt: r.DepartureAt.UTC(),
			EndsAt:   r.ReturnAt.UTC(),
			Done:     r.Status == statusApproved,
			Path:     "/expenses/claims/" + strconv.FormatInt(r.ID, 10),
		}
		if r.Destination != nil {
			item.Subtitle = *r.Destination
		}
		items = append(items, item)
	}
	return items, nil
}
