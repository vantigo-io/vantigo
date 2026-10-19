package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// GetCalendarColleaguesByUserIdItems A colleague's calendar
// (GET /api/v1/calendar/colleagues/{userId}/items)
//
// When a colleague can be booked (calendar design D6): their appointments
// that block time — owned, or attended and accepted, with transparency busy —
// expanded as GET /items expands them. A shared one carries its title, and
// its location as the subtitle; a private one carries neither, only
// visibility private: the "Busy" a person reads is the client's word in the
// client's language, not this module's. A free one is left out, since the
// view answers "when can I book them". No source is asked — a colleague's
// follow-ups, tasks and hours are the other modules' data behind their own
// doors — so sources is empty. The colleague must be an active user in the
// directory, or this is the 404 an unknown id gets; the caller's own id is
// answered as their own calendar would be, private titles and free time
// included, so the client need not special-case it. The router has enforced
// calendar:view-colleagues; there is no manager relation to consult.
func (s *server) GetCalendarColleaguesByUserIdItems(ctx context.Context, req gen.GetCalendarColleaguesByUserIdItemsRequestObject) (gen.GetCalendarColleaguesByUserIdItemsResponseObject, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	w, f, err := s.parseWindow(ctx, req.Params.From, req.Params.To, req.Params.TimeZone, c.zone())
	if err != nil {
		return nil, err
	}
	if f.failed() {
		return gen.GetCalendarColleaguesByUserIdItems400ApplicationProblemPlusJSONResponse(invalid(invalidWindowTitle, f)), nil
	}
	none := []gen.CalendarSourceKind{}
	if req.UserId == c.UserID {
		own, err := s.ownItems(ctx, c.UserID, w)
		if err != nil {
			return nil, err
		}
		return gen.GetCalendarColleaguesByUserIdItems200JSONResponse(windowOut(w, none, append([]gen.CalendarItem{}, own...))), nil
	}
	active, err := s.userActive(ctx, req.UserId)
	if err != nil {
		return nil, fmt.Errorf("calendar: look up colleague %s: %w", req.UserId, err)
	}
	if !active {
		return gen.GetCalendarColleaguesByUserIdItems404Response{}, nil
	}
	rows, err := s.queries().ListColleagueBusyAppointmentsInWindow(ctx, store.ListColleagueBusyAppointmentsInWindowParams{
		UserID: req.UserId, FromAt: w.fromAt.Add(-24 * time.Hour), ToAt: w.toAt.Add(24 * time.Hour),
		FromDate: pgDate(addDays(w.from, -seriesReachDays)), ToDate: pgDate(addDays(w.to, 1)),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: read colleague %s's appointments: %w", req.UserId, err)
	}
	list, err := s.expandItems(ctx, rows, w, func(store.CalendarAppointment) itemView { return itemView{} })
	if err != nil {
		return nil, err
	}
	return gen.GetCalendarColleaguesByUserIdItems200JSONResponse(windowOut(w, none, append([]gen.CalendarItem{}, list...))), nil
}
