package calendar

import (
	"context"
	"errors"
	"fmt"

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
// answer.
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
	)
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if _, err := txq.GetAppointmentForUpdate(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return fmt.Errorf("calendar: lock appointment %d: %w", req.Id, err)
		}
		now := s.deps.Clock()
		_, err := txq.SetAttendeeStatus(ctx, store.SetAttendeeStatusParams{
			AppointmentID: req.Id, UserID: me, Status: string(body.Status), Now: now,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return fmt.Errorf("calendar: record an answer to appointment %d: %w", req.Id, err)
		}
		if row, err = txq.TouchAppointment(ctx, store.TouchAppointmentParams{ID: req.Id, Now: now}); err != nil {
			return fmt.Errorf("calendar: touch appointment %d: %w", req.Id, err)
		}
		if atts, err = txq.ListAttendees(ctx, req.Id); err != nil {
			return fmt.Errorf("calendar: read appointment %d's attendees: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.PostCalendarAppointmentsByIdResponse404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respond(ctx, row, atts, me)
	if err != nil {
		return nil, err
	}
	return gen.PostCalendarAppointmentsByIdResponse200JSONResponse(out), nil
}
