package calendar

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is the delete of an appointment (calendar invitations design
// D6), which becomes a cancellation once someone outside has been told of
// it. A cancelled appointment keeps its row — answered by GET with
// cancelledAt, absent from GET /items and the colleague view (the window
// queries leave it out) — and is read-only: every write to it is 409
// appointment_cancelled (lockWritable, and the answer and resend handlers).
// The calendar-cancellations worker erases it after its last day
// (cancellations_worker.go).

// DeleteCalendarAppointmentsById Delete an appointment
// (DELETE /api/v1/calendar/appointments/{id})
//
// When no outside attendee has ever been sent an invitation, the series goes,
// its attendees and exceptions with it (ON DELETE CASCADE), and each
// colleague is told it was cancelled, so none sees it vanish untold.
// Otherwise it is cancelled: cancelled_at set and the sequence bumped under
// the row's lock, then, after the commit, a CANCEL to every outside attendee
// who had been sent an invitation and the notice to every colleague. A cancelled series keeps its exceptions,
// so the CANCEL names what it cancels.
func (s *server) DeleteCalendarAppointmentsById(ctx context.Context, req gen.DeleteCalendarAppointmentsByIdRequestObject) (gen.DeleteCalendarAppointmentsByIdResponseObject, error) {
	err := s.deleteAppointment(ctx, callerID(ctx), req.Id)
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.DeleteCalendarAppointmentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.DeleteCalendarAppointmentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteCalendarAppointmentsById204Response{}, nil
}

// deleteAppointment deletes, or cancels, the appointment id me owns — the
// screen's DELETE and the Outlook write-back's delete of a copy (calendar
// Outlook sync design O10.8) alike, as the DELETE's comment above tells —
// and, after the commit, sends the CANCELs and the notices. Its refusals: an
// appointment me does not own 404, a cancelled one 409
// appointment_cancelled.
func (s *server) deleteAppointment(ctx context.Context, me uuid.UUID, id int64) error {
	var (
		row       store.CalendarAppointment
		atts      []store.CalendarAppointmentAttendee
		excs      []store.CalendarAppointmentException
		cancelled bool
	)
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		var err error
		if row, err = lockWritable(ctx, txq, id, me); err != nil {
			return err
		}
		if atts, err = txq.ListAttendees(ctx, row.ID); err != nil {
			return fmt.Errorf("calendar: read appointment %d's attendees: %w", row.ID, err)
		}
		if err := s.nudgeMailboxes(ctx, txq, row.OwnerUserID, atts); err != nil {
			return err
		}
		if !anyInvited(atts) {
			if _, err := txq.DeleteAppointment(ctx, row.ID); err != nil {
				return fmt.Errorf("calendar: delete appointment %d: %w", row.ID, err)
			}
			return nil
		}
		if excs, err = exceptionsOf(ctx, txq, row); err != nil {
			return err
		}
		if row, err = txq.CancelAppointment(ctx, store.CancelAppointmentParams{ID: row.ID, CancelledAt: s.deps.Clock()}); err != nil {
			return fmt.Errorf("calendar: cancel appointment %d: %w", id, err)
		}
		cancelled = true
		return nil
	})
	if err != nil {
		return err
	}
	b := s.newBudget(ctx, afterCommitBudget)
	defer b.done()
	if cancelled {
		s.sendInvitations(b, invitationJob{appt: row, excs: excs, listed: atts, to: atts, method: methodCancel})
	}
	s.notifyUsers(b, row, userAttendees(atts), noticeCancelled)
	return nil
}

// anyInvited reports whether an outside attendee has ever been sent an
// invitation — the line between a delete and a cancellation.
func anyInvited(atts []store.CalendarAppointmentAttendee) bool {
	for _, at := range atts {
		if isOutside(at) && at.InvitedAt != nil {
			return true
		}
	}
	return false
}
