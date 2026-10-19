package calendar

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// PostCalendarRsvpRead Read an invitation for the RSVP page
// (POST /api/v1/calendar/rsvp/read)
//
// TODO(calendar invitations, Task 3): the RSVP page's read by the token's
// hash, anonymous and rate limited (calendar invitations design D5).
func (s *server) PostCalendarRsvpRead(context.Context, gen.PostCalendarRsvpReadRequestObject) (gen.PostCalendarRsvpReadResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostCalendarRsvp Answer an invitation
// (POST /api/v1/calendar/rsvp)
//
// TODO(calendar invitations, Task 3): an invitee's answer through the RSVP
// link, anonymous and rate limited (calendar invitations design D5).
func (s *server) PostCalendarRsvp(context.Context, gen.PostCalendarRsvpRequestObject) (gen.PostCalendarRsvpResponseObject, error) {
	return nil, module.ErrNotImplemented
}
