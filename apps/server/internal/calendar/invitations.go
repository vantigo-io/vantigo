package calendar

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend Resend an invitation
// (POST /api/v1/calendar/appointments/{id}/attendees/{attendeeId}/resend)
//
// TODO(calendar invitations, Task 3): one outside attendee's invitation sent
// again by the owner (calendar invitations design D3).
func (s *server) PostCalendarAppointmentsByIdAttendeesByAttendeeIdResend(context.Context, gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResendRequestObject) (gen.PostCalendarAppointmentsByIdAttendeesByAttendeeIdResendResponseObject, error) {
	return nil, module.ErrNotImplemented
}
