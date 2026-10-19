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
// calendar, and every kind the enabled modules' sources declare — for the
// filter, the legend and the colours — which costs no provider a query.
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
	return gen.GetCalendarMeta200JSONResponse{
		TimeZone:          c.zone().String(),
		Today:             apiDate(today),
		CanViewColleagues: c.CanViewColleagues,
		Sources:           kinds,
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
