package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// PostCalendarAppointmentsByIdResponse Answer an appointment
// (POST /api/v1/calendar/appointments/{id}/response)
//
// An attendee's answer — accepted, declined or tentative — which they may
// change at any time (calendar design D3). Anyone who is not an attendee gets
// the 404 an unknown id gets, the owner included: their own status is
// implicitly accepted and never stored, so there is nothing of theirs to
// answer. An attendee's answer to a cancelled appointment is 409
// appointment_cancelled (calendar invitations design D6).
func (s *server) PostCalendarAppointmentsByIdResponse(ctx context.Context, req gen.PostCalendarAppointmentsByIdResponseRequestObject) (gen.PostCalendarAppointmentsByIdResponseResponseObject, error) {
	var body gen.AppointmentResponseWrite
	if req.Body != nil {
		body = *req.Body
	}
	if !body.Status.Valid() {
		var f fieldErrors
		f.add("status", "status is accepted, declined or tentative")
		return gen.PostCalendarAppointmentsByIdResponse400ApplicationProblemPlusJSONResponse(invalid(invalidResponseTitle, f)), nil
	}
	me := callerID(ctx)
	var (
		row  store.CalendarAppointment
		atts []store.CalendarAppointmentAttendee
		excs []store.CalendarAppointmentException
	)
	// An answer changes nothing of the appointment itself — not its time, its
	// place or its text — so neither its revision nor its sequence moves, and
	// the owner's If-Match read before it still holds.
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		var err error
		row, err = txq.GetAppointmentForUpdate(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return fmt.Errorf("calendar: lock appointment %d: %w", req.Id, err)
		}
		if row.CancelledAt != nil {
			before, err := txq.ListAttendees(ctx, req.Id)
			if err != nil {
				return fmt.Errorf("calendar: read appointment %d's attendees: %w", req.Id, err)
			}
			if row.OwnerUserID == me || !mayRead(row, before, me) {
				return errNotFound
			}
			return errCancelled(row)
		}
		_, err = txq.SetAttendeeStatus(ctx, store.SetAttendeeStatusParams{
			AppointmentID: req.Id, UserID: me, Status: string(body.Status), Now: s.deps.Clock(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return fmt.Errorf("calendar: record an answer to appointment %d: %w", req.Id, err)
		}
		if atts, err = txq.ListAttendees(ctx, req.Id); err != nil {
			return fmt.Errorf("calendar: read appointment %d's attendees: %w", req.Id, err)
		}
		if err := s.nudgeMailboxes(ctx, txq, row.OwnerUserID, atts); err != nil {
			return err
		}
		excs, err = exceptionsOf(ctx, txq, row)
		return err
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostCalendarAppointmentsByIdResponse409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostCalendarAppointmentsByIdResponse404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respond(ctx, row, atts, excs, me)
	if err != nil {
		return nil, err
	}
	return gen.PostCalendarAppointmentsByIdResponse200JSONResponse(out), nil
}
