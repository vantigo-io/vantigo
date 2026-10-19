package calendar

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// This file is the feed itself (calendar feed design D2–D5): GET
// /feed/{token}.ics, the module's one anonymous GET with its token in the
// path, answering the person's calendar as iCalendar.

// GetCalendarFeedByFile answers a calendar address's feed.
func (s *server) GetCalendarFeedByFile(context.Context, gen.GetCalendarFeedByFileRequestObject) (gen.GetCalendarFeedByFileResponseObject, error) {
	return nil, errNotImplemented
}
