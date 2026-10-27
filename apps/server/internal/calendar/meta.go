package calendar

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// GetCalendarMeta The calendar's settings
// (GET /api/v1/calendar/meta)
//
// What the app needs before it asks for a window (calendar design D3): the
// business zone and today in it, whether the caller may open a colleague's
// calendar, whether invitations are mailed (calendar invitations design D3:
// the mail driver is smtp), and every kind the enabled modules' sources declare — for the
// filter, the legend and the colours — which costs no provider a query, with
// calendar.subscription after them when the caller subscribes to a calendar,
// and calendar.outlook last while the caller's Outlook sync is on.
func (s *server) GetCalendarMeta(ctx context.Context, _ gen.GetCalendarMetaRequestObject) (gen.GetCalendarMetaResponseObject, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	today, err := s.calendarToday(ctx, s.deps.Clock())
	if err != nil {
		return nil, fmt.Errorf("calendar: read today: %w", err)
	}
	kinds, err := s.sourceKinds(ctx)
	if err != nil {
		return nil, err
	}
	// The subscriptions' chip (calendar feed design D8), after the
	// providers', when the caller has a subscription to show.
	n, err := s.queries().CountSubscriptions(ctx, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("calendar: count the subscriptions: %w", err)
	}
	if n > 0 {
		kinds = append(kinds, gen.CalendarSourceKind{Kind: subscriptionKind, Module: "calendar"})
	}
	// The Outlook busy time's chip (calendar Outlook sync design O9.5), while
	// the caller's sync is on.
	row, err := s.mailboxSyncRow(ctx, s.queries(), c.UserID, false)
	if err != nil {
		return nil, err
	}
	if row != nil && row.Enabled {
		kinds = append(kinds, gen.CalendarSourceKind{Kind: outlookKind, Module: "calendar"})
	}
	return gen.GetCalendarMeta200JSONResponse{
		TimeZone:             c.zone().String(),
		Today:                apiDate(today),
		CanViewColleagues:    c.CanViewColleagues,
		InvitationsAvailable: s.mailAvailable(),
		Sources:              kinds,
	}, nil
}

// sourceKinds is every kind a wired source declares, in module order.
func (s *server) sourceKinds(ctx context.Context) ([]gen.CalendarSourceKind, error) {
	srcs, err := s.sources(ctx)
	if err != nil {
		return nil, err
	}
	out := []gen.CalendarSourceKind{}
	for _, src := range srcs {
		for _, k := range src.CalendarKinds() {
			out = append(out, gen.CalendarSourceKind{Kind: k.Key, Module: k.Module})
		}
	}
	return out, nil
}
